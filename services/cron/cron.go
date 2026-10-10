// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cron

import (
	"context"
	"runtime/pprof"
	"time"

	"forgejo.org/modules/graceful"
	"forgejo.org/modules/log"
	"forgejo.org/modules/process"
	"forgejo.org/modules/sync"
	"forgejo.org/modules/translation"

	"github.com/go-co-op/gocron/v2"
)

var scheduler gocron.Scheduler

// Prevent duplicate running tasks.
var taskStatusTable = sync.NewStatusTable()

// NewContext begins cron tasks
// Each cron task is run within the shutdown context as a running server
// AtShutdown the cron server is stopped
func NewContext(original context.Context) {
	defer pprof.SetGoroutineLabels(original)
	_, _, finished := process.GetManager().AddTypedContext(graceful.GetManager().ShutdownContext(), "Service: Cron", process.SystemProcessType, true)

	var err error
	scheduler, err = gocron.NewScheduler()
	if err != nil {
		log.Error("Could not start cron scheduler: %v ", err)
		return
	}

	initBasicTasks()
	initExtendedTasks()
	initActionsTasks()

	lock.Lock()
	for _, task := range tasks {
		if task.IsEnabled() && task.DoRunAtStart() {
			go task.Run()
		}
	}

	scheduler.Start()
	started = true
	lock.Unlock()
	graceful.GetManager().RunAtShutdown(context.Background(), func() {
		if err := scheduler.Shutdown(); err != nil {
			log.Error("No clean shutdown of cron scheduler: %v", err)
		}
		lock.Lock()
		started = false
		lock.Unlock()
		finished()
	})
}

// TaskTableRow represents a task row in the tasks table
type TaskTableRow struct {
	Name        string
	Spec        string
	Next        time.Time
	Prev        time.Time
	Status      string
	LastMessage string
	LastDoer    string
	ExecTimes   int64
	task        *Task
}

func (t *TaskTableRow) FormatLastMessage(locale translation.Locale) string {
	if t.Status == "finished" {
		return t.task.GetConfig().FormatMessage(locale, t.Name, t.Status, t.LastDoer)
	}

	return t.task.GetConfig().FormatMessage(locale, t.Name, t.Status, t.LastDoer, t.LastMessage)
}

// TaskTable represents a table of tasks
type TaskTable []*TaskTableRow

// ListTasks returns all running cron tasks.
func ListTasks() TaskTable {
	jobs := scheduler.Jobs()
	jobMap := map[string]gocron.Job{}
	for _, job := range jobs {
		jobMap[job.Name()] = job
	}

	lock.Lock()
	defer lock.Unlock()

	tTable := make([]*TaskTableRow, 0, len(tasks))
	for _, task := range tasks {
		spec := "-"
		var (
			next time.Time
			prev time.Time
		)
		if e, ok := jobMap[task.Name]; ok {
			if e.Schedule().JobType() == gocron.CronJobType {
				spec = e.Schedule().(gocron.CronJobSchedule).Crontab
			}
			var err error
			next, err = e.NextRun()
			if err != nil {
				log.Warn("NextRun() returned error for %q: %v", task.Name, err)
			}
			prev, err = e.LastRunStartedAt()
			if err != nil {
				log.Warn("LastRunStartedAt() returned error for %q: %v", task.Name, err)
			}
		}

		task.lock.Lock()
		// If the manual run is after the cron run, use that instead.
		if prev.Before(task.LastRun) {
			prev = task.LastRun
		}
		tTable = append(tTable, &TaskTableRow{
			Name:        task.Name,
			Spec:        spec,
			Next:        next,
			Prev:        prev,
			ExecTimes:   task.ExecTimes,
			LastMessage: task.LastMessage,
			Status:      task.Status,
			LastDoer:    task.LastDoer,
			task:        task,
		})
		task.lock.Unlock()
	}

	return tTable
}

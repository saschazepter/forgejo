// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cron

import (
	"cmp"
	"slices"
	"strconv"
	"testing"

	"forgejo.org/modules/test"
	"github.com/go-co-op/gocron/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddTaskToScheduler(t *testing.T) {
	defer test.MockProtect(&scheduler)()

	var err error
	scheduler, err = gocron.NewScheduler()
	require.NoError(t, err)

	// no seconds
	err = addTaskToScheduler(&Task{
		Name: "task 1",
		config: &BaseConfig{
			Schedule: "5 4 * * *",
		},
	})
	require.NoError(t, err)
	jobs := scheduler.Jobs()
	assert.Len(t, jobs, 1)
	assert.Equal(t, "task 1", jobs[0].Name())
	assert.Equal(t, "5 4 * * *", jobs[0].Schedule().(gocron.CronJobSchedule).Crontab)

	// with seconds
	err = addTaskToScheduler(&Task{
		Name: "task 2",
		config: &BaseConfig{
			Schedule: "30 5 4 * * *",
		},
	})
	require.NoError(t, err)
	jobs = scheduler.Jobs() // the item order is not guaranteed, so we need to sort it before "assert"
	slices.SortFunc(jobs, func(a, b gocron.Job) int {
		return cmp.Compare(a.Name(), b.Name())
	})
	assert.Len(t, jobs, 2)
	assert.Equal(t, "task 2", jobs[1].Name())
	assert.Equal(t, "30 5 4 * * *", jobs[1].Schedule().(gocron.CronJobSchedule).Crontab)
}

func TestScheduleHasSeconds(t *testing.T) {
	tests := []struct {
		schedule  string
		hasSecond bool
	}{
		{"* * * * * *", true},
		{"* * * * *", false},
		{"5 4 * * *", false},
		{"5 4 * * *", false},
		{"5,8 4 * * *", false},
		{"*   *   *  * * *", true},
		{"5,8 4   *  *   *", false},
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, test.hasSecond, scheduleHasSeconds(test.schedule))
		})
	}
}

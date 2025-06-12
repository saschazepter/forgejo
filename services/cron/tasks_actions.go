// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cron

import (
	"context"
	"time"

	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	actions_service "forgejo.org/services/actions"
)

func initActionsTasks() {
	if !setting.Actions.Enabled {
		return
	}
	registerStopZombieTasks()
	registerStopEndlessTasks()
	registerCancelAbandonedJobs()
	registerScheduleTasks()
	registerActionsCleanup()
	registerOfflineRunnersCleanup()
}

func registerStopZombieTasks() {
	RegisterTaskFatal("stop_zombie_tasks", &BaseConfig{
		Enabled:    true,
		RunAtStart: true,
		Schedule:   "@every 5m",
	}, func(ctx context.Context, _ *user_model.User, cfg Config) error {
		return actions_service.StopZombieTasks(ctx)
	})
}

func registerStopEndlessTasks() {
	RegisterTaskFatal("stop_endless_tasks", &BaseConfig{
		Enabled:    true,
		RunAtStart: true,
		Schedule:   "@every 30m",
	}, func(ctx context.Context, _ *user_model.User, cfg Config) error {
		return actions_service.StopEndlessTasks(ctx)
	})
}

func registerCancelAbandonedJobs() {
	RegisterTaskFatal("cancel_abandoned_jobs", &BaseConfig{
		Enabled:    true,
		RunAtStart: true,
		Schedule:   "@every 6h",
	}, func(ctx context.Context, _ *user_model.User, cfg Config) error {
		return actions_service.CancelAbandonedJobs(ctx)
	})
}

// registerScheduleTasks registers a scheduled task that runs every minute to start any due schedule tasks.
func registerScheduleTasks() {
	// Register the task with a unique name, enabled status, and schedule for every minute.
	RegisterTaskFatal("start_schedule_tasks", &BaseConfig{
		Enabled:    true,
		RunAtStart: false,
		Schedule:   "@every 1m",
	}, func(ctx context.Context, _ *user_model.User, cfg Config) error {
		// Call the function to start schedule tasks and pass the context.
		return actions_service.StartScheduleTasks(ctx)
	})
}

func registerActionsCleanup() {
	RegisterTaskFatal("cleanup_actions", &BaseConfig{
		Enabled:    true,
		RunAtStart: false,
		Schedule:   "@midnight",
	}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return actions_service.Cleanup(ctx)
	})
}

func registerOfflineRunnersCleanup() {
	RegisterTaskFatal("cleanup_offline_runners", &CleanupOfflineRunnersConfig{
		BaseConfig: BaseConfig{
			Enabled:    false,
			RunAtStart: false,
			Schedule:   "@midnight",
		},
		GlobalScopeOnly: true,
		OlderThan:       time.Hour * 24,
	}, func(ctx context.Context, _ *user_model.User, cfg Config) error {
		c := cfg.(*CleanupOfflineRunnersConfig)
		return actions_service.CleanupOfflineRunners(
			ctx,
			c.OlderThan,
			c.GlobalScopeOnly,
		)
	})
}

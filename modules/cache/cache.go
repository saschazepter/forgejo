// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cache

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"forgejo.org/modules/setting"

	mc "code.forgejo.org/go-chi/cache"

	_ "code.forgejo.org/go-chi/cache/memcache" // memcache plugin for cache
)

var conn mc.Cache

func newCache(cacheConfig setting.Cache) (mc.Cache, error) {
	return mc.NewCacher(mc.Options{
		Adapter:       cacheConfig.Adapter,
		AdapterConfig: cacheConfig.Conn,
		Interval:      cacheConfig.Interval,
	})
}

// Init start cache service
func Init() error {
	var err error

	if conn == nil {
		if conn, err = newCache(setting.CacheService.Cache); err != nil {
			return err
		}
		if err = conn.Ping(); err != nil {
			return err
		}
	}

	return err
}

const (
	testCacheKey       = "DefaultCache.TestKey"
	SlowCacheThreshold = 100 * time.Microsecond
)

func Test() (time.Duration, error) {
	if conn == nil {
		return 0, errors.New("default cache not initialized")
	}

	testData := fmt.Sprintf("%x", make([]byte, 500))

	start := time.Now()

	if err := conn.Delete(testCacheKey); err != nil {
		return 0, fmt.Errorf("expect cache to delete data based on key if exist but got: %w", err)
	}
	if err := conn.Put(testCacheKey, testData, 10); err != nil {
		return 0, fmt.Errorf("expect cache to store data but got: %w", err)
	}
	testVal := conn.Get(testCacheKey)
	if testVal == nil {
		return 0, errors.New("expect cache hit but got none")
	}
	if testVal != testData {
		return 0, errors.New("expect cache to return same value as stored but got other")
	}

	return time.Since(start), nil
}

// GetCache returns the currently configured cache
func GetCache() mc.Cache {
	return conn
}

// GetString returns the key value from cache with callback when no key exists in cache
func GetString(key string, getFunc func() (string, error)) (string, error) {
	if conn == nil || setting.CacheService.TTL == 0 {
		return getFunc()
	}

	cached := conn.Get(key)

	if cached == nil {
		value, err := getFunc()
		if err != nil {
			return value, err
		}
		return value, conn.Put(key, value, setting.CacheService.TTLSeconds())
	}

	if value, ok := cached.(string); ok {
		return value, nil
	}

	if stringer, ok := cached.(fmt.Stringer); ok {
		return stringer.String(), nil
	}

	return fmt.Sprintf("%s", cached), nil
}

// GetInt returns key value from cache with callback when no key exists in cache
func GetInt(key string, getFunc func() (int, error)) (int, error) {
	if conn == nil || setting.CacheService.TTL == 0 {
		return getFunc()
	}

	cached := conn.Get(key)

	if cached == nil {
		value, err := getFunc()
		if err != nil {
			return value, err
		}

		return value, conn.Put(key, value, setting.CacheService.TTLSeconds())
	}

	switch v := cached.(type) {
	case int:
		return v, nil
	case string:
		value, err := strconv.Atoi(v)
		if err != nil {
			return 0, err
		}
		return value, nil
	default:
		value, err := getFunc()
		if err != nil {
			return value, err
		}
		return value, conn.Put(key, value, setting.CacheService.TTLSeconds())
	}
}

// GetInt64 returns key value from cache with callback when no key exists in cache
func GetInt64(key string, getFunc func() (int64, error)) (int64, error) {
	if conn == nil || setting.CacheService.TTL == 0 {
		return getFunc()
	}

	cached := conn.Get(key)

	if cached == nil {
		value, err := getFunc()
		if err != nil {
			return value, err
		}

		return value, conn.Put(key, value, setting.CacheService.TTLSeconds())
	}

	switch v := conn.Get(key).(type) {
	case int64:
		return v, nil
	case string:
		value, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, err
		}
		return value, nil
	default:
		value, err := getFunc()
		if err != nil {
			return value, err
		}

		return value, conn.Put(key, value, setting.CacheService.TTLSeconds())
	}
}

// Remove key from cache
func Remove(key string) {
	if conn == nil {
		return
	}
	_ = conn.Delete(key)
}

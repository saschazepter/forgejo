// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"bufio"
	"os"
	"strings"

	"forgejo.org/modules/log"

	"github.com/gobwas/glob"
)

var (
	changesetFiles     []string
	changesetAvailable bool
	globalFullRun      = false
)

func initChangedFiles() {
	globalFullRun = os.Getenv("RUN_ALL") != ""
	if globalFullRun {
		log.Info("Full run of all tests requested via RUN_ALL environment.")
		return
	}
	var changes string
	changes, changesetAvailable = os.LookupEnv("CHANGED_FILES")
	// the output of the Action seems to actually contain \n and not a newline literal
	changesetFiles = strings.Split(changes, `\n`)
	log.Info("Only running tests covered by a subset of test files. Received the following list of CHANGED_FILES: %q", changesetFiles)

	globalPatterns := []string{
		// meta and config
		"Makefile",
		"playwright.config.ts",
		".forgejo/workflows/testing.yml",
		"tests/e2e/*.go",
		"tests/e2e/shared/*",
		// frontend files
		"web_src/js/{index,utils}.*",
		"web_src/css/{base,index}.css",
		// templates and helpers
		"templates/base/**",
		"modules/templates/**",
	}
	fullRunPatterns := []glob.Glob{}
	for _, expr := range globalPatterns {
		fullRunPatterns = append(fullRunPatterns, glob.MustCompile(expr, '.', '/'))
	}

	for _, changedFile := range changesetFiles {
		for _, pattern := range fullRunPatterns {
			if pattern.Match(changedFile) {
				globalFullRun = true
				log.Info("Changed files match global test pattern, running all tests")
				return
			}
		}
	}
}

func canSkipTest(testFile string) bool {
	// run all tests when environment variable is not set or changes match global pattern
	if !changesetAvailable || globalFullRun {
		return false
	}

	for _, changedFile := range changesetFiles {
		if strings.HasSuffix(testFile, changedFile) {
			return false
		}
		for _, pattern := range getWatchPatterns(testFile) {
			if pattern.Match(changedFile) {
				return false
			}
		}
	}
	return true
}

func getWatchPatterns(filename string) []glob.Glob {
	file, err := os.Open(filename)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)

	watchSection := false
	patterns := []glob.Glob{}
	for scanner.Scan() {
		line := scanner.Text()
		// check for watch block
		if strings.HasPrefix(line, "// @watch") {
			if watchSection {
				break
			}
			watchSection = true
		}
		if !watchSection {
			continue
		}

		line = strings.TrimPrefix(line, "// ")
		if line != "" {
			globPattern, err := glob.Compile(line, '.', '/')
			if err != nil {
				log.Fatal("Invalid glob pattern '%s' (skipped): %v", line, err)
			}
			patterns = append(patterns, globPattern)
		}
	}
	// if no watch block in file
	if !watchSection {
		patterns = append(patterns, glob.MustCompile("*"))
	}
	return patterns
}

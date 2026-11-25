// Copyright 2023 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package translation

import (
	"os"
	"path/filepath"
	"testing"

	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/translation/i18n"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkdir(t *testing.T, elems ...string) {
	require.NoError(t, os.MkdirAll(filepath.Join(elems...), 0o755))
}

func write(t *testing.T, content string, elems ...string) {
	require.NoError(t, os.WriteFile(filepath.Join(elems...), []byte(content), 0o644))
}

func TestTrSize(t *testing.T) {
	l := NewLocale("")
	size := int64(1)
	assert.Equal(t, "1 munits.data.b", l.TrSize(size).String())
	size *= 2048
	assert.Equal(t, "2 munits.data.kib", l.TrSize(size).String())
	size *= 2048
	assert.Equal(t, "4 munits.data.mib", l.TrSize(size).String())
	size *= 2048
	assert.Equal(t, "8 munits.data.gib", l.TrSize(size).String())
	size *= 2048
	assert.Equal(t, "16 munits.data.tib", l.TrSize(size).String())
	size *= 2048
	assert.Equal(t, "32 munits.data.pib", l.TrSize(size).String())
	size *= 128
	assert.Equal(t, "4 munits.data.eib", l.TrSize(size).String())
}

func TestPrettyNumber(t *testing.T) {
	i18n.ResetDefaultLocales()

	allLangMap = make(map[string]*LangType)
	allLangMap["id-ID"] = &LangType{Lang: "id-ID", Name: "Bahasa Indonesia"}

	l := NewLocale("id-ID")
	assert.Equal(t, "1.000.000", l.PrettyNumber(1000000))
	assert.Equal(t, "1.000.000,1", l.PrettyNumber(1000000.1))
	assert.Equal(t, "1.000.000", l.PrettyNumber("1000000"))
	assert.Equal(t, "1.000.000", l.PrettyNumber("1000000.0"))
	assert.Equal(t, "1.000.000,1", l.PrettyNumber("1000000.1"))

	l = NewLocale("nosuch")
	assert.Equal(t, "1,000,000", l.PrettyNumber(1000000))
	assert.Equal(t, "1,000,000.1", l.PrettyNumber(1000000.1))
}

// maps vals to keys, stops when end of values is reached, expects len(val) <= len(keys)
func makeTestMap(keys, vals []string) map[string]string {
	res := make(map[string]string, len(keys))
	for i, val := range vals {
		res[keys[i]] = val
	}
	return res
}

// returns the path to the custom dir
func makeDirs(t *testing.T) (baseDir, custom, builtInLn, customLn string) {
	baseDir = t.TempDir()
	options := baseDir + "/" + "options"
	custom = baseDir + "/" + "custom"
	builtInLn = options + "/" + "locale_next"
	customLn = custom + "/" + "options" + "/" + "locale_next"
	mkdir(t, builtInLn)
	mkdir(t, customLn)
	return baseDir, custom, builtInLn, customLn
}

// creates a map of keys and vals, marshals to json, writes file to location
func writeFileAndContent(t *testing.T, keys, vals []string, location, filename string) {
	m := makeTestMap(keys, vals)
	bytes, err := json.Marshal(m)
	require.NoError(t, err)
	write(t, string(bytes), location, filename)
}

func TestLocalePrecedence(t *testing.T) {
	keys := []string{"test.one", "test.two", "test.three", "test.four", "test.custom", "test.other"}
	valsDEAlt := []string{"einse", "zweie", "dreie", "viere", "eigene", "andere"}
	valsDE := []string{"eins", "zwei", "drei", "vier", "eigen", "anderes"}
	valsENAlt := []string{"1", "2", "3", "4", "cust", "otr"}
	valsEN := []string{"one", "two", "three", "four", "custom", "other"}
	filenameDE := "locale_de-DE.json"
	filenameEN := "locale_en-US.json"
	localeDE := "de-DE"
	localeEN := "en-US"
	setting.Names = []string{"English", "German"}
	setting.Langs = []string{"en-US", "de-DE"}

	for _, tt := range []struct {
		name               string
		custom1            []string // The custom translation to take primary precedence
		custom1FN          string
		langToUse          string // Lang name of translation to take precedence
		nonEnglishBultin   []string
		nonEnglishBultinFN string
		custom2            []string // The custom translation to take secondary precedence
		custom2FN          string
		englishBuiltin     []string
		expected           []string
		expectedMix        [][]string
	}{ // Some possible file and language constellations - which custom files could be present, and which language is set
		{
			name:               "custom non-english precedence",
			custom2:            valsENAlt,
			custom2FN:          filenameEN,
			nonEnglishBultin:   valsDE,
			nonEnglishBultinFN: filenameDE,
			englishBuiltin:     valsEN,
			custom1:            valsDEAlt,
			custom1FN:          filenameDE,
			langToUse:          localeDE,
			expected:           valsDEAlt,
			expectedMix:        [][]string{valsDE, valsDEAlt},
		},
		{
			name:               "builtin non-english precedence, custom english present",
			custom2:            valsENAlt,
			custom2FN:          filenameEN,
			nonEnglishBultin:   valsDE,
			nonEnglishBultinFN: filenameDE,
			englishBuiltin:     valsEN,
			custom1:            []string{},
			langToUse:          localeDE,
			expected:           valsDE,
			expectedMix:        [][]string{valsDE, valsENAlt},
		},
		{
			name:               "custom english precedence",
			custom2:            []string{},
			nonEnglishBultin:   valsDE,
			nonEnglishBultinFN: filenameDE,
			englishBuiltin:     valsEN,
			custom1:            valsENAlt,
			custom1FN:          filenameEN,
			langToUse:          localeEN,
			expected:           valsENAlt,
			expectedMix:        [][]string{valsEN, valsENAlt},
		},
	} { // Test key replacements in the custom locale files against the different file constellations
		t.Run(tt.name+" replace one key", func(t *testing.T) {
			baseDir, custom, builtInLn, customLn := makeDirs(t)
			setting.StaticRootPath = baseDir
			setting.CustomPath = custom
			if len(tt.custom1) > 0 {
				writeFileAndContent(t, keys, tt.custom1[:1], customLn, tt.custom1FN)
			}
			if len(tt.nonEnglishBultin) > 0 {
				writeFileAndContent(t, keys, tt.nonEnglishBultin, builtInLn, tt.nonEnglishBultinFN)
			}
			if len(tt.custom2) > 0 {
				writeFileAndContent(t, keys, tt.custom2[:1], customLn, tt.custom2FN)
			}
			if len(tt.englishBuiltin) > 0 {
				writeFileAndContent(t, keys, tt.englishBuiltin, builtInLn, filenameEN)
			}
			refreshLocales()
			tl, _ := i18n.DefaultLocales.Locale(tt.langToUse)
			// The custom key replaces the existing key
			assert.Equal(t, tt.expected[0], tl.TrString(keys[0]))
			// Other keys are not affected
			switch tt.langToUse {
			case localeDE:
				assert.Equal(t, tt.nonEnglishBultin[1], tl.TrString(keys[1]))
			case localeEN:
				assert.Equal(t, tt.englishBuiltin[1], tl.TrString(keys[1]))
			}
		})
		t.Run(tt.name+" replace multiple keys", func(t *testing.T) {
			baseDir, custom, builtInLn, customLn := makeDirs(t)
			setting.StaticRootPath = baseDir
			setting.CustomPath = custom
			max := 3
			if len(tt.custom1) > 0 {
				writeFileAndContent(t, keys, tt.custom1[:max], customLn, tt.custom1FN)
			}
			if len(tt.nonEnglishBultin) > 0 {
				writeFileAndContent(t, keys, tt.nonEnglishBultin, builtInLn, filenameDE)
			}
			if len(tt.custom2) > 0 {
				writeFileAndContent(t, keys, tt.custom2[:max], customLn, tt.custom2FN)
			}
			if len(tt.englishBuiltin) > 0 {
				writeFileAndContent(t, keys, tt.englishBuiltin, builtInLn, filenameEN)
			}
			refreshLocales()
			tl, _ := i18n.DefaultLocales.Locale(tt.langToUse)
			for i, val := range tt.expected[:max] {
				assert.Equal(t, val, tl.TrString(keys[i]))
			}
			// Other keys are not affected
			switch tt.langToUse {
			case localeDE:
				assert.Equal(t, tt.nonEnglishBultin[max+1], tl.TrString(keys[max+1]))
			case localeEN:
				assert.Equal(t, tt.englishBuiltin[max+1], tl.TrString(keys[max+1]))
			}
		})
		t.Run(tt.name+" replace all keys", func(t *testing.T) {
			baseDir, custom, builtInLn, customLn := makeDirs(t)
			setting.StaticRootPath = baseDir
			setting.CustomPath = custom
			if len(tt.custom1) > 0 {
				writeFileAndContent(t, keys, tt.custom1, customLn, tt.custom1FN)
			}
			if len(tt.nonEnglishBultin) > 0 {
				writeFileAndContent(t, keys, tt.nonEnglishBultin, builtInLn, tt.nonEnglishBultinFN)
			}
			if len(tt.custom2) > 0 {
				writeFileAndContent(t, keys, tt.custom2, customLn, tt.custom2FN)
			}
			if len(tt.englishBuiltin) > 0 {
				writeFileAndContent(t, keys, tt.englishBuiltin, builtInLn, filenameEN)
			}
			refreshLocales()
			tl, _ := i18n.DefaultLocales.Locale(tt.langToUse)
			for i, val := range tt.expected {
				assert.Equal(t, val, tl.TrString(keys[i]))
			}
		})
		t.Run(tt.name+" add additional keys", func(t *testing.T) {
			baseDir, custom, builtInLn, customLn := makeDirs(t)
			setting.StaticRootPath = baseDir
			setting.CustomPath = custom
			split := 2
			if len(tt.custom1) > 0 {
				// Additional 3 keys are present in custom1
				writeFileAndContent(t, keys[split:], tt.custom1[split:], customLn, tt.custom1FN)
			}
			if len(tt.nonEnglishBultin) > 0 {
				// First three keys in base should be present
				writeFileAndContent(t, keys[:split], tt.nonEnglishBultin[:split], builtInLn, tt.nonEnglishBultinFN)
			}
			if len(tt.custom2) > 0 {
				// Additional 3 keys are present in custom2
				writeFileAndContent(t, keys[split:], tt.custom2[split:], customLn, tt.custom2FN)
			}
			if len(tt.englishBuiltin) > 0 {
				// First three keys in base should be present
				writeFileAndContent(t, keys[:split], tt.englishBuiltin[:split], builtInLn, filenameEN)
			}
			refreshLocales()
			tl, _ := i18n.DefaultLocales.Locale(tt.langToUse)
			// If keys are present in custom locale, that are not present in builtin locale we expect a mix
			// The mix might be multi lingual if the custom locale of the base lang is missing
			expectedMix := tt.expectedMix[0][:split]
			expectedMix = append(expectedMix, tt.expectedMix[1][split:]...)
			for i, val := range expectedMix {
				assert.Equal(t, val, tl.TrString(keys[i]))
			}
		})
	}
}

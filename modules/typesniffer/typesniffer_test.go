// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package typesniffer

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectContentTypeLongerThanSniffLen(t *testing.T) {
	// Pre-condition: Shorter than sniffLen detects SVG.
	assert.Equal(t, "image/svg+xml", DetectContentType([]byte(`<!-- Comment --><svg></svg>`), "").contentType)
	// Longer than sniffLen detects something else.
	assert.NotEqual(t, "image/svg+xml", DetectContentType([]byte(`<!-- `+strings.Repeat("x", sniffLen)+` --><svg></svg>`), "").contentType)
}

func TestIsTextFile(t *testing.T) {
	assert.True(t, DetectContentType([]byte{}, "").IsText())
	assert.True(t, DetectContentType([]byte("lorem ipsum"), "").IsText())
}

func TestIsSvgImage(t *testing.T) {
	assert.True(t, DetectContentType([]byte("<svg></svg>"), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte("    <svg></svg>"), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<svg width="100"></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<?xml version="1.0" encoding="UTF-8"?><svg></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<!-- Comment -->
	<svg></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<!-- Multiple -->
	<!-- Comments -->
	<svg></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<!-- Multiline
	Comment -->
	<svg></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1 Basic//EN"
	"http://www.w3.org/Graphics/SVG/1.1/DTD/svg11-basic.dtd">
	<svg></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<?xml version="1.0" encoding="UTF-8"?>
	<!-- Comment -->
	<svg></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<?xml version="1.0" encoding="UTF-8"?>
	<!-- Multiple -->
	<!-- Comments -->
	<svg></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<?xml version="1.0" encoding="UTF-8"?>
	<!-- Multiline
	Comment -->
	<svg></svg>`), "").IsSvgImage())
	assert.True(t, DetectContentType([]byte(`<?xml version="1.0" encoding="UTF-8"?>
	<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">
	<!-- Multiline
	Comment -->
	<svg></svg>`), "").IsSvgImage())

	// the DetectContentType should work for incomplete data, because only beginning bytes are used for detection
	assert.True(t, DetectContentType([]byte(`<svg>....`), "").IsSvgImage())

	assert.False(t, DetectContentType([]byte{}, "").IsSvgImage())
	assert.False(t, DetectContentType([]byte("svg"), "").IsSvgImage())
	assert.False(t, DetectContentType([]byte("<svgfoo></svgfoo>"), "").IsSvgImage())
	assert.False(t, DetectContentType([]byte("text<svg></svg>"), "").IsSvgImage())
	assert.False(t, DetectContentType([]byte("<html><body><svg></svg></body></html>"), "").IsSvgImage())
	assert.False(t, DetectContentType([]byte(`<script>"<svg></svg>"</script>`), "").IsSvgImage())
	assert.False(t, DetectContentType([]byte(`<!-- <svg></svg> inside comment -->
	<foo></foo>`), "").IsSvgImage())
	assert.False(t, DetectContentType([]byte(`<?xml version="1.0" encoding="UTF-8"?>
	<!-- <svg></svg> inside comment -->
	<foo></foo>`), "").IsSvgImage())

	assert.False(t, DetectContentType([]byte(`
<!-- comment1 -->
<div>
	<!-- comment2 -->
	<svg></svg>
</div>
`), "").IsSvgImage())

	assert.False(t, DetectContentType([]byte(`
<!-- comment1
-->
<div>
	<!-- comment2
-->
	<svg></svg>
</div>
`), "").IsSvgImage())
	assert.False(t, DetectContentType([]byte(`<html><body><!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd"><svg></svg></body></html>`), "").IsSvgImage())
	assert.False(t, DetectContentType([]byte(`<html><body><?xml version="1.0" encoding="UTF-8"?><svg></svg></body></html>`), "").IsSvgImage())
}

func TestIsPDF(t *testing.T) {
	pdf, _ := base64.StdEncoding.DecodeString("JVBERi0xLjYKJcOkw7zDtsOfCjIgMCBvYmoKPDwvTGVuZ3RoIDMgMCBSL0ZpbHRlci9GbGF0ZURlY29kZT4+CnN0cmVhbQp4nF3NPwsCMQwF8D2f4s2CNYk1baF0EHRwOwg4iJt/NsFb/PpevUE4Mjwe")
	assert.True(t, DetectContentType(pdf, "").IsPDF())
	assert.False(t, DetectContentType([]byte("plain text"), "").IsPDF())
}

func TestIsVideo(t *testing.T) {
	mp4, _ := base64.StdEncoding.DecodeString("AAAAGGZ0eXBtcDQyAAAAAGlzb21tcDQyAAEI721vb3YAAABsbXZoZAAAAADaBlwX2gZcFwAAA+gA")
	assert.True(t, DetectContentType(mp4, "").IsVideo())
	assert.False(t, DetectContentType([]byte("plain text"), "").IsVideo())
}

func TestIsAudio(t *testing.T) {
	mp3, _ := base64.StdEncoding.DecodeString("SUQzBAAAAAABAFRYWFgAAAASAAADbWFqb3JfYnJhbmQAbXA0MgBUWFhYAAAAEQAAA21pbm9yX3Zl")
	assert.True(t, DetectContentType(mp3, "").IsAudio())
	assert.False(t, DetectContentType([]byte("plain text"), "").IsAudio())

	assert.True(t, DetectContentType([]byte("ID3Toy\000"), "").IsAudio())
	assert.True(t, DetectContentType([]byte("ID3Toy\n====\t* hi 🌞, ..."), "").IsText())          // test ID3 tag for plain text
	assert.True(t, DetectContentType([]byte("ID3Toy\n====\t* hi 🌞, ..."+"🌛"[0:2]), "").IsText()) // test ID3 tag with incomplete UTF8 char
}

func TestIsGLB(t *testing.T) {
	glb, _ := hex.DecodeString("676c5446")
	assert.True(t, DetectContentType(glb, "").IsGLB())
	assert.True(t, DetectContentType(glb, "").Is3DModel())
	assert.False(t, DetectContentType([]byte("plain text"), "").IsGLB())
	assert.False(t, DetectContentType([]byte("plain text"), "").Is3DModel())
}

func TestIsFont(t *testing.T) {
	ttf := []byte{0x00, 0x01, 0x00, 0x00, 0x00}
	assert.True(t, DetectContentType(ttf, "font.ttf").IsFont())
	assert.Equal(t, "font/ttf", DetectContentType(ttf, "font.ttf").GetMimeType())

	assert.True(t, DetectContentType([]byte("OTTO\x00\x00"), "font.otf").IsFont())
	assert.True(t, DetectContentType([]byte("wOFF\x00\x00"), "font.woff").IsFont())
	assert.True(t, DetectContentType([]byte("wOF2\x00\x00"), "font.woff2").IsFont())
	assert.True(t, DetectContentType([]byte("ttcf\x00\x00"), "font.ttc").IsFont())

	assert.False(t, DetectContentType([]byte("plain text"), "").IsFont())
}

func TestDetectContentTypeFromReader(t *testing.T) {
	mp3, _ := base64.StdEncoding.DecodeString("SUQzBAAAAAABAFRYWFgAAAASAAADbWFqb3JfYnJhbmQAbXA0MgBUWFhYAAAAEQAAA21pbm9yX3Zl")
	st, err := DetectContentTypeFromReader(bytes.NewReader(mp3), "")
	require.NoError(t, err)
	assert.True(t, st.IsAudio())
}

func TestDetectContentTypeOgg(t *testing.T) {
	oggAudio, _ := hex.DecodeString("4f67675300020000000000000000352f0000000000007dc39163011e01766f72626973000000000244ac0000000000000071020000000000b8014f6767530000")
	st, err := DetectContentTypeFromReader(bytes.NewReader(oggAudio), "")
	require.NoError(t, err)
	assert.True(t, st.IsAudio())

	oggVideo, _ := hex.DecodeString("4f676753000200000000000000007d9747ef000000009b59daf3012a807468656f7261030201001e00110001e000010e00020000001e00000001000001000001")
	st, err = DetectContentTypeFromReader(bytes.NewReader(oggVideo), "")
	require.NoError(t, err)
	assert.True(t, st.IsVideo())
}

func TestDetectContentTypeAvif(t *testing.T) {
	avifImage, err := hex.DecodeString("000000206674797061766966")
	require.NoError(t, err)

	st, err := DetectContentTypeFromReader(bytes.NewReader(avifImage), "")
	require.NoError(t, err)

	assert.True(t, st.IsImage())
}

func TestDetectContentTypeJxl(t *testing.T) {
	jxlImage, err := hex.DecodeString("0000000c4a584c200d0a870a00000014667479706a786c20")
	require.NoError(t, err)

	st, err := DetectContentTypeFromReader(bytes.NewReader(jxlImage), "")
	require.NoError(t, err)

	assert.True(t, st.IsImage())
}

func TestDetectContentTypeModelGLB(t *testing.T) {
	glb, err := hex.DecodeString("676c5446")
	require.NoError(t, err)

	st, err := DetectContentTypeFromReader(bytes.NewReader(glb), "")
	require.NoError(t, err)

	// print st for debugging
	assert.Equal(t, "model/gltf-binary", st.GetMimeType())
	assert.True(t, st.IsGLB())
}

func TestDetectInterlisp(t *testing.T) {
	interlisp, err := base64.StdEncoding.DecodeString("ICAKKERFRklORS1GSUxFLUlORk8gHlBBQ0tBR0UgIklOVEVSTElTUCIgHlJFQURUQUJMRSAiSU5URVJMSVNQIiAeQkFTRSAxMCkKCgYB")
	require.NoError(t, err)
	st, err := DetectContentTypeFromReader(bytes.NewReader(interlisp), "test")
	require.NoError(t, err)
	assert.True(t, st.IsText())
	st, err = DetectContentTypeFromReader(bytes.NewReader(interlisp), "")
	require.NoError(t, err)
	assert.False(t, st.IsText())
	st, err = DetectContentTypeFromReader(bytes.NewReader(interlisp), "test.lcom")
	require.NoError(t, err)
	assert.False(t, st.IsText())
}

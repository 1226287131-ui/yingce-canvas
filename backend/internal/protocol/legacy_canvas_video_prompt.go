package protocol

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The old canvas exposed @参考图1 / @Image1 style markers in the video prompt.
// Its compatible providers still depend on those markers, so the declarative
// plugin keeps the conversion at the protocol boundary instead of placing a
// provider-specific rule in Canvas or Agent UI.
var legacyCanvasV1ReferencePattern = regexp.MustCompile(`[@＠](?:参考图|图片|Image)([0-9]+)`)
var legacyCanvasV2ReferencePattern = regexp.MustCompile(`[@＠](参考图|图片|Image|参考视频|视频|Video|参考音频|音频|Audio)([0-9]+)`)

func compileLegacyCanvasVideoV1Prompt(prompt string, imageCount int) (string, error) {
	text := strings.ReplaceAll(prompt, "＠", "@")
	invalid := make(map[string]struct{})
	text = replaceLegacyCanvasReferenceTokens(text, legacyCanvasV1ReferencePattern, func(token string) string {
		match := legacyCanvasV1ReferencePattern.FindStringSubmatch(token)
		index, ok := legacyCanvasReferenceIndex(match, imageCount)
		if !ok {
			invalid[token] = struct{}{}
			return token
		}
		return fmt.Sprintf("第%d张参考图（REFERENCE_%d）", index+1, index)
	})
	if len(invalid) > 0 {
		return "", fmt.Errorf("无效参考图引用：%s", legacyCanvasInvalidTokens(invalid))
	}
	if imageCount == 0 {
		return text, nil
	}
	mapping := make([]string, 0, imageCount)
	for index := 0; index < imageCount; index++ {
		mapping = append(mapping, fmt.Sprintf("第%d张参考图对应 REFERENCE_%d", index+1, index))
	}
	return "参考图严格按传入顺序编号：" + strings.Join(mapping, "；") + "。请严格按照这些编号理解下方指令，不要混淆不同参考图。\n\n" + text, nil
}

func compileLegacyCanvasVideoV2Prompt(prompt string, images, videos, audios int) (string, error) {
	text := strings.ReplaceAll(prompt, "＠", "@")
	invalid := make(map[string]struct{})
	text = replaceLegacyCanvasReferenceTokens(text, legacyCanvasV2ReferencePattern, func(token string) string {
		match := legacyCanvasV2ReferencePattern.FindStringSubmatch(token)
		if len(match) != 3 {
			return token
		}
		kind, max := legacyCanvasV2ReferenceKind(match[1], images, videos, audios)
		index, ok := legacyCanvasReferenceIndex(match, max)
		if !ok {
			invalid[token] = struct{}{}
			return token
		}
		return fmt.Sprintf("@%s%d", kind, index+1)
	})
	if len(invalid) > 0 {
		return "", fmt.Errorf("无效参考资产引用：%s", legacyCanvasInvalidTokens(invalid))
	}
	return text, nil
}

func legacyCanvasReferenceIndex(match []string, max int) (int, bool) {
	if len(match) == 0 {
		return 0, false
	}
	value := match[len(match)-1]
	index, err := strconv.Atoi(value)
	if err != nil || index < 1 || index > max || value != strconv.Itoa(index) {
		return 0, false
	}
	return index - 1, true
}

func legacyCanvasV2ReferenceKind(value string, images, videos, audios int) (string, int) {
	switch value {
	case "参考图", "图片", "Image":
		return "Image", images
	case "参考视频", "视频", "Video":
		return "Video", videos
	default:
		return "Audio", audios
	}
}

func legacyCanvasInvalidTokens(values map[string]struct{}) string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	return strings.Join(items, "、")
}

// Go regexps have no lookbehind. Check the old JS token boundaries explicitly
// so email addresses and identifiers such as @Image1abc remain literal text.
func replaceLegacyCanvasReferenceTokens(text string, pattern *regexp.Regexp, replace func(string) string) string {
	var result strings.Builder
	previous := 0
	for _, match := range pattern.FindAllStringIndex(text, -1) {
		start, end := match[0], match[1]
		if start > 0 && strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._%+-", rune(text[start-1])) {
			continue
		}
		if end < len(text) {
			next, _ := utf8.DecodeRuneInString(text[end:])
			if unicode.IsNumber(next) || strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_", next) {
				continue
			}
			if next == '.' && end+1 < len(text) && strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_", rune(text[end+1])) {
				continue
			}
		}
		result.WriteString(text[previous:start])
		result.WriteString(replace(text[start:end]))
		previous = end
	}
	result.WriteString(text[previous:])
	return result.String()
}

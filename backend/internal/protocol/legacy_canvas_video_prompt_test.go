package protocol

import "testing"

func TestLegacyCanvasVideoPromptTokenBoundaries(t *testing.T) {
	for _, text := range []string{"mail@Image1.com", "@Image1abc", "@Image1_foo", "@图片1２", "tag-@Video1", "@Audio1.mp3"} {
		got, err := compileLegacyCanvasVideoV2Prompt(text, 0, 0, 0)
		if err != nil || got != text {
			t.Fatalf("literal %q became %q: %v", text, got, err)
		}
	}
	for _, text := range []string{"@图片0", "@图片01", "@参考视频2", "@音频1"} {
		if _, err := compileLegacyCanvasVideoV2Prompt(text, 1, 1, 0); err == nil {
			t.Fatalf("invalid token accepted: %s", text)
		}
	}
	got, err := compileLegacyCanvasVideoV2Prompt("（@图片1），@视频1。", 1, 1, 0)
	if err != nil || got != "（@Image1），@Video1。" {
		t.Fatalf("punctuation = %q: %v", got, err)
	}
}

func TestLegacyCanvasVideoPromptTransforms(t *testing.T) {
	v1, err := compileLegacyCanvasVideoV1Prompt("让＠图片2向@Image1走去", 2)
	if err != nil || v1 != "参考图严格按传入顺序编号：第1张参考图对应 REFERENCE_0；第2张参考图对应 REFERENCE_1。请严格按照这些编号理解下方指令，不要混淆不同参考图。\n\n让第2张参考图（REFERENCE_1）向第1张参考图（REFERENCE_0）走去" {
		t.Fatalf("V1 prompt = %q, err = %v", v1, err)
	}
	v2, err := compileLegacyCanvasVideoV2Prompt("@参考图1跟随＠视频1配合@音频1", 1, 1, 1)
	if err != nil || v2 != "@Image1跟随@Video1配合@Audio1" {
		t.Fatalf("V2 prompt = %q, err = %v", v2, err)
	}
	if _, err := compileLegacyCanvasVideoV2Prompt("@参考视频2", 0, 1, 0); err == nil {
		t.Fatal("invalid media marker was accepted")
	}
}

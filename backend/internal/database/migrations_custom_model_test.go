package database

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestMigrateSchemaV58PreservesMemorySettings(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:migration-custom-agent-model-v58?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	old := model.AgentMemorySetting{UserID: "owner", CompactInterval: model.AgentMemoryCompactIntervalWeekly, ChannelID: "managed-channel", Model: "managed-model"}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&model.AgentMemorySetting{}, "CustomModelJSON"); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", 58).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("upgrade from schema 57: %v", err)
	}
	var upgraded model.AgentMemorySetting
	if err := db.First(&upgraded, "user_id = ?", old.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if upgraded.CompactInterval != old.CompactInterval || upgraded.ChannelID != old.ChannelID || upgraded.Model != old.Model || upgraded.CustomModelJSON != "" {
		t.Fatalf("existing memory settings changed during migration: %#v", upgraded)
	}
	if err := db.Model(&upgraded).Update("custom_model_json", `{"config":{"apiKey":"enc:fixture"}}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var repeated model.AgentMemorySetting
	if err := db.First(&repeated, "user_id = ?", old.UserID).Error; err != nil || repeated.CustomModelJSON == "" {
		t.Fatalf("repeat migration lost custom model snapshot: %v", err)
	}
}

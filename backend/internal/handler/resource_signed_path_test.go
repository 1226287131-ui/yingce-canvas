package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/assets"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestPublicResourceSignedPathHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://media.example.test")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources", "fixture"), []byte("media-data"), 0640); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.New(db), dir)
	router := gin.New()
	RegisterUserDataRoutes(router.Group("/api"), svc)
	for i, media := range []struct{ mime, extension string }{
		{"image/jpeg", "jpg"}, {"image/png", "png"}, {"video/mp4", "mp4"}, {"audio/mpeg", "mp3"},
	} {
		t.Run(media.extension, func(t *testing.T) {
			resource := model.Resource{ID: fmt.Sprintf("r%d", i), UserID: "owner", Kind: strings.Split(media.mime, "/")[0], Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "fixture", MimeType: media.mime, Size: 10}
			if err := db.Create(&resource).Error; err != nil {
				t.Fatal(err)
			}
			items, err := svc.ResourceAccessBatch("owner", []service.ResourceAccessRequest{{ResourceID: resource.ID, AccessOptions: assets.AccessOptions{Purpose: assets.PurposeDisplay, Variant: assets.VariantOriginal}}})
			if err != nil || len(items) != 1 || items[0].Access == nil {
				t.Fatalf("access: %#v %v", items, err)
			}
			u, err := url.Parse(items[0].Access.URL)
			if err != nil {
				t.Fatal(err)
			}
			legacy := u.RequestURI()
			query := u.Query()
			u.Path = fmt.Sprintf("/api/public/resources/%s/media/%s/%s/%s/resource.%s", resource.ID, query.Get("variant"), query.Get("expires"), query.Get("signature"), media.extension)
			u.RawQuery = ""
			if u.Fragment != "" || !strings.HasSuffix(u.String(), "."+media.extension) {
				t.Fatalf("invalid suffix: %s", u)
			}
			get := func(path string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Range", "bytes=0-3")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w
			}
			w := get(u.RequestURI())
			if w.Code != 206 || w.Body.String() != "medi" || w.Header().Get("Content-Type") != media.mime {
				t.Fatalf("delivery: %d %v %q", w.Code, w.Header(), w.Body.String())
			}
			parts := strings.Split(u.Path, "/")
			for _, index := range []int{4, 6, 7, 8} {
				altered := append([]string(nil), parts...)
				altered[index] = "invalid"
				if got := get(strings.Join(altered, "/")).Code; got < 400 {
					t.Fatalf("tampering segment %d accepted: %d", index, got)
				}
			}
			if w := get(legacy); w.Code != 206 {
				t.Fatalf("legacy delivery: %d", w.Code)
			}
		})
	}
}

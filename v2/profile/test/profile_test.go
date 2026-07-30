package test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hiddify/hiddify-core/v2/profile"
	"github.com/sagernet/sing-box/experimental/libbox"
)

// fixtureWarpProfile is a frozen snapshot of the WARP test config this suite has always used
// (previously fetched live from GitHub on every test run - see plans/031 for why that changed).
//
// NOTE: when this fixture was captured (2026-07-30), the live GitHub content had already drifted
// from what the original TestAddByContent assertions expected: the profile title decodes to
// "🔥Psiphon & WARP 🔥" (base64 `8J+UpVBzaXBob24gJiBXQVJQIPCflKU=`), not the
// previously-asserted "🔥 WARP 🔥". The assertion below has been updated to match
// the actual fetched content (source of truth), per plans/031's STOP-condition guidance.
const fixtureWarpProfile = `//profile-title: base64:8J+UpVBzaXBob24gJiBXQVJQIPCflKU=
//profile-update-interval: 24
//subscription-userinfo: upload=0; download=0; total=10737418240000000; expire=2546249531
//support-url: https://t.me/hiddify
//profile-web-page-url: https://hiddify.com

psiphon://auto/
#psiphon://auto/?region=...&remote_server_list_url=...&remote_server_list_download_filename=...&remote_server_list_signature_public_key=...
warp://A1@188.114.97.170:894#warp_in_warp -> warp://A2@188.114.97.170:894?ifp=40-80&ifps=40-100&ifpd=4-8&ifpm=m4#m4 
warp://B1@auto#WarpInWarp✅ -> warp://B2@auto?ifpm=m4#LocalIP
#warp://p1@ip1?ifp=1-3#WarpInWarp✅ -> warp://p2@ip2ifp=1-3#Warp🇮🇷IP

#warp://auto?ifp=10-20&ifps=40-100&ifpd=10-20#Warp_10-20_40-100_10-20
#warp://auto?ifp=10-20&ifps=40-100&ifpd=30-200#Warp_10-20_40-100_30-50
#warp://auto?ifp=10-20&ifps=40-100&ifpd=300-500#Warp_10-20_40-100_300-500


#warp://auto?ifp=5-10&ifps=40-100&ifpd=10-20#Warp_5-10_40-100_10-20
#warp://auto?ifp=5-10&ifps=40-100&ifpd=30-50#Warp_5-10_40-100_30-50
#warp://auto?ifp=5-10&ifps=40-100&ifpd=300-500#Warp_5-10_40-100_300-500



`

func TestAddByContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixtureWarpProfile))
	}))
	defer server.Close()

	ctx := libbox.BaseContext(nil)
	entity, err := profile.AddByUrl(ctx, server.URL, "", false)
	if err != nil {
		t.Fatalf("expected no error, but got: %v", err)
	}
	defer profile.DeleteById(entity.Id)

	// Check if the content has been added correctly
	profileTitle := entity.Name
	expectedTitle := "🔥Psiphon & WARP 🔥" // The Base64 decoded title (see fixtureWarpProfile comment)
	if profileTitle != expectedTitle {
		t.Errorf("expected profile title to be %v, got %v", expectedTitle, profileTitle)
	}

	// Check subscription userinfo
	userInfo := entity.SubInfo
	if userInfo.Upload != 0 || userInfo.Download != 0 || userInfo.Total != 10737418240000000 || userInfo.Expire != 2546249531 {
		t.Errorf("subscription userinfo not parsed correctly, got: %v", userInfo)
	}

	// Check URLs
	supportURL := entity.SubInfo.SupportUrl
	if supportURL != "https://t.me/hiddify" {
		t.Errorf("expected support URL to be https://t.me/hiddify, got %v", supportURL)
	}

	profileWebPageURL := entity.SubInfo.WebPageUrl
	if profileWebPageURL != "https://hiddify.com" {
		t.Errorf("expected profile web page URL to be https://hiddify.com, got %v", profileWebPageURL)
	}
	// You can further assert individual fields of warp configurations
}

func TestGetByName_And_GetByUrl(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixtureWarpProfile))
	}))
	defer server.Close()

	ctx := libbox.BaseContext(nil)
	entity, err := profile.AddByUrl(ctx, server.URL, "", false)
	if err != nil {
		t.Fatalf("AddByUrl failed: %v", err)
	}
	defer profile.DeleteById(entity.Id)

	t.Run("GetByUrl finds the profile by its exact URL", func(t *testing.T) {
		found, err := profile.GetByUrl(ctx, server.URL)
		if err != nil {
			t.Fatalf("GetByUrl failed: %v", err)
		}
		if found.Id != entity.Id {
			t.Errorf("GetByUrl returned id %q, want %q", found.Id, entity.Id)
		}
	})

	t.Run("GetByName finds the profile by its parsed name", func(t *testing.T) {
		found, err := profile.GetByName(entity.Name)
		if err != nil {
			t.Fatalf("GetByName failed: %v", err)
		}
		if found.Id != entity.Id {
			t.Errorf("GetByName returned id %q, want %q", found.Id, entity.Id)
		}
	})

	t.Run("GetByUrl returns an error for an unknown URL", func(t *testing.T) {
		_, err := profile.GetByUrl(ctx, "https://example.invalid/does-not-exist")
		if err == nil {
			t.Error("expected an error for a URL with no matching profile")
		}
	})
}

// TestDeleteById_NonexistentId documents DeleteById's actual current behavior for a
// missing ID: per profile_repository.go, DeleteById calls table.Delete(id), which
// delegates to the underlying LevelDB handle's Delete - a no-op for a missing key
// under standard LevelDB semantics, so no error is returned.
func TestDeleteById_NonexistentId(t *testing.T) {
	err := profile.DeleteById("this-id-does-not-exist")
	if err != nil {
		t.Errorf("expected DeleteById to be a no-op (nil error) for a nonexistent id, got: %v", err)
	}
}

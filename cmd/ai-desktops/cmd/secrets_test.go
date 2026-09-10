package cmd

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func secretScriptRequest(t *testing.T, script string) []byte {
	t.Helper()
	parts := strings.SplitN(script, "'", 3)
	if len(parts) != 3 {
		t.Fatal("missing encoded metadata")
	}
	data, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSecretScriptsEncodeMetadataWithoutShellExpansion(t *testing.T) {
	path := "/test/$(must-not-run)"
	script := buildSecretsReloadScript([]string{path}, "us-east-2")
	if strings.Contains(script, path) {
		t.Fatal("unquoted metadata in shell")
	}
	var request struct {
		Paths  []string
		Region string
	}
	if err := json.Unmarshal(secretScriptRequest(t, script), &request); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Paths, []string{path}) || request.Region != "us-east-2" {
		t.Fatalf("wrong metadata: %#v", request)
	}
	if buildSecretsRemoveReloadScript([]string{path}, "us-east-2") != script {
		t.Fatal("remove must use the same transactional rotation")
	}
	if strings.Contains(string(secretScriptRequest(t, buildSecretsClearScript())), "null") {
		t.Fatal("clear must encode an empty list")
	}
}

func TestDesktopSecretPathsTracksAgentAndPreservesOverrideOrder(t *testing.T) {
	for _, tc := range []struct {
		agent        string
		extras, want []string
	}{
		{"/agents", nil, []string{"/agents"}},
		{"/agents", []string{"/override", "/agents", "/override"}, []string{"/agents", "/override"}},
		{"", []string{"/custom"}, []string{"/custom"}},
		{"", nil, nil},
	} {
		if got := desktopSecretPaths(tc.agent, tc.extras); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("paths = %v, want %v", got, tc.want)
		}
	}
}

func TestSecretPathsAfterAdd_ReloadsExistingAndNewSecrets(t *testing.T) {
	toAdd, reloadPaths := secretPathsAfterAdd(
		[]string{"/markcallen/smoke"},
		[]string{"/orchael/desktops/local"},
	)

	if want := []string{"/orchael/desktops/local"}; !reflect.DeepEqual(toAdd, want) {
		t.Fatalf("toAdd = %#v, want %#v", toAdd, want)
	}
	if want := []string{"/markcallen/smoke", "/orchael/desktops/local"}; !reflect.DeepEqual(reloadPaths, want) {
		t.Fatalf("reloadPaths = %#v, want %#v", reloadPaths, want)
	}

	script := string(secretScriptRequest(t, buildSecretsReloadScript(reloadPaths, "us-east-2")))
	for _, want := range reloadPaths {
		if !strings.Contains(script, want) {
			t.Fatalf("reload script missing %q:\n%s", want, script)
		}
	}
}

func TestSecretPathsAfterAdd_DeduplicatesExistingAndNewSecrets(t *testing.T) {
	toAdd, reloadPaths := secretPathsAfterAdd(
		[]string{"/existing"},
		[]string{"/existing", "/new", "/new"},
	)

	if want := []string{"/new"}; !reflect.DeepEqual(toAdd, want) {
		t.Fatalf("toAdd = %#v, want %#v", toAdd, want)
	}
	if want := []string{"/existing", "/new"}; !reflect.DeepEqual(reloadPaths, want) {
		t.Fatalf("reloadPaths = %#v, want %#v", reloadPaths, want)
	}
}

func TestSecretPathsAfterRemove_RemovesRequestedPaths(t *testing.T) {
	toRemove, remainingPaths := secretPathsAfterRemove(
		[]string{"/one", "/two", "/three"},
		[]string{"/two", "/missing", "/two"},
	)

	if want := []string{"/two"}; !reflect.DeepEqual(toRemove, want) {
		t.Fatalf("toRemove = %#v, want %#v", toRemove, want)
	}
	if want := []string{"/one", "/three"}; !reflect.DeepEqual(remainingPaths, want) {
		t.Fatalf("remainingPaths = %#v, want %#v", remainingPaths, want)
	}
}

func TestSecretPathsAfterRemove_ReloadScriptExcludesRemovedPaths(t *testing.T) {
	toRemove, remainingPaths := secretPathsAfterRemove(
		[]string{"/markcallen/smoke", "/orchael/desktops/local", "/ai-desktops/dev/control-plane/aws-operator"},
		[]string{"/ai-desktops/dev/control-plane/aws-operator"},
	)

	if want := []string{"/ai-desktops/dev/control-plane/aws-operator"}; !reflect.DeepEqual(toRemove, want) {
		t.Fatalf("toRemove = %#v, want %#v", toRemove, want)
	}

	script := string(secretScriptRequest(t, buildSecretsReloadScript(remainingPaths, "us-east-2")))
	if strings.Contains(script, "/ai-desktops/dev/control-plane/aws-operator") {
		t.Fatalf("reload script includes removed secret:\n%s", script)
	}
	for _, want := range []string{"/markcallen/smoke", "/orchael/desktops/local"} {
		if !strings.Contains(script, want) {
			t.Fatalf("reload script missing remaining secret %q:\n%s", want, script)
		}
	}
}

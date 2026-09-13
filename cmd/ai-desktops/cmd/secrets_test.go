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
	script := buildSecretsReloadScript("/test/agents", []string{path}, "us-east-2")
	if strings.Contains(script, path) {
		t.Fatal("unquoted metadata in shell")
	}
	var request struct {
		AgentPath string   `json:"agent_path"`
		Paths     []string `json:"paths"`
		Region    string   `json:"region"`
	}
	if err := json.Unmarshal(secretScriptRequest(t, script), &request); err != nil {
		t.Fatal(err)
	}
	if request.AgentPath != "/test/agents" || !reflect.DeepEqual(request.Paths, []string{path}) || request.Region != "us-east-2" {
		t.Fatalf("wrong metadata: %#v", request)
	}
	if buildSecretsRemoveReloadScript("/test/agents", []string{path}, "us-east-2") != script {
		t.Fatal("remove must use the same transactional rotation")
	}
	if strings.Contains(string(secretScriptRequest(t, buildSecretsClearScript())), "null") {
		t.Fatal("clear must encode an empty list")
	}
}

func TestRuntimeSecretPathsSeparatesTrackedAgentSecret(t *testing.T) {
	for _, tc := range []struct {
		name, configuredAgent, wantAgent string
		tracked, wantDesktop             []string
	}{
		{name: "tracked-agent", configuredAgent: "/agents", tracked: []string{"/override", "/agents", "/application"}, wantAgent: "/agents", wantDesktop: []string{"/override", "/application"}},
		{name: "legacy-agent-not-tracked", configuredAgent: "/agents", tracked: []string{"/application"}, wantDesktop: []string{"/application"}},
		{name: "create-filters-explicit-duplicate", configuredAgent: "/agents", tracked: desktopSecretPaths("/agents", []string{"/agents", "/application"}), wantAgent: "/agents", wantDesktop: []string{"/application"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, desktop := runtimeSecretPaths(tc.configuredAgent, tc.tracked)
			if agent != tc.wantAgent {
				t.Fatalf("agent path = %q, want %q", agent, tc.wantAgent)
			}
			if !reflect.DeepEqual(desktop, tc.wantDesktop) {
				t.Fatalf("desktop paths = %v, want %v", desktop, tc.wantDesktop)
			}
			if got := runtimeDesktopSecretPaths(tc.configuredAgent, tc.tracked); !reflect.DeepEqual(got, tc.wantDesktop) {
				t.Fatalf("create-time desktop paths = %v, want %v", got, tc.wantDesktop)
			}
		})
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

func TestTrackedSecretPathsPreservesBasePrecedenceWithoutAddingSecrets(t *testing.T) {
	for _, tc := range []struct {
		name        string
		paths, want []string
	}{
		{"legacy-agent-added-last", []string{"/override", "/agents"}, []string{"/agents", "/override"}},
		{"agent-not-registered", []string{"/override"}, []string{"/override"}},
		{"already-ordered", []string{"/agents", "/one", "/two"}, []string{"/agents", "/one", "/two"}},
		{"no-paths", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trackedSecretPaths("/agents", tc.paths); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateSecretsReloadRequirements(t *testing.T) {
	paths := []string{"/agents", "/override"}
	for _, tc := range []struct {
		name, requiredPath, requiredRegion, desktopRegion, wantError string
	}{
		{name: "exact-target", requiredPath: "/override", requiredRegion: "us-east-2", desktopRegion: "us-east-2"},
		{name: "untracked-path", requiredPath: "/other", requiredRegion: "us-east-2", desktopRegion: "us-east-2", wantError: "not configured"},
		{name: "wrong-region", requiredPath: "/override", requiredRegion: "us-west-2", desktopRegion: "us-east-2", wantError: "region mismatch"},
		{name: "ordinary-reload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSecretsReloadRequirements(paths, tc.desktopRegion, tc.requiredPath, tc.requiredRegion)
			if tc.wantError == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantError)
			}
		})
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

	agent, desktop := runtimeSecretPaths("", reloadPaths)
	script := string(secretScriptRequest(t, buildSecretsReloadScript(agent, desktop, "us-east-2")))
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

	agent, desktop := runtimeSecretPaths("", remainingPaths)
	script := string(secretScriptRequest(t, buildSecretsReloadScript(agent, desktop, "us-east-2")))
	if strings.Contains(script, "/ai-desktops/dev/control-plane/aws-operator") {
		t.Fatalf("reload script includes removed secret:\n%s", script)
	}
	for _, want := range []string{"/markcallen/smoke", "/orchael/desktops/local"} {
		if !strings.Contains(script, want) {
			t.Fatalf("reload script missing remaining secret %q:\n%s", want, script)
		}
	}
}

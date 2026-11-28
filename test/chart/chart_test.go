package chart_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"helm.sh/helm/v4/pkg/chart/common"
	commonutil "helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/engine"
	"helm.sh/helm/v4/pkg/strvals"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

const (
	releaseName      = "qdrant-operator"
	releaseNamespace = "qdrant-system"
	leaderElection   = "qdrant-operator-leader-election"
)

var documentSeparator = regexp.MustCompile(`(?m)^---\s*$`)

func render(t *testing.T, release string, sets ...string) ([]*unstructured.Unstructured, error) {
	t.Helper()
	chart, err := loader.LoadDir(filepath.Join("..", "..", "charts", "qdrant-operator"))
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	overrides := map[string]any{}
	for _, set := range sets {
		if err := strvals.ParseInto(set, overrides); err != nil {
			t.Fatalf("parse --set %q: %v", set, err)
		}
	}
	options := common.ReleaseOptions{Name: release, Namespace: releaseNamespace, IsInstall: true, Revision: 1}
	values, err := commonutil.ToRenderValues(chart, overrides, options, nil)
	if err != nil {
		t.Fatalf("render values: %v", err)
	}
	files, err := engine.Render(chart, values)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var objects []*unstructured.Unstructured
	for _, name := range names {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		for _, document := range documentSeparator.Split(files[name], -1) {
			if strings.TrimSpace(document) == "" {
				continue
			}
			object := &unstructured.Unstructured{}
			if err := yaml.Unmarshal([]byte(document), &object.Object); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			if len(object.Object) == 0 {
				continue
			}
			objects = append(objects, object)
		}
	}
	return objects, nil
}

func mustRender(t *testing.T, sets ...string) []*unstructured.Unstructured {
	t.Helper()
	objects, err := render(t, releaseName, sets...)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return objects
}

func ofKind(objects []*unstructured.Unstructured, kind string) []*unstructured.Unstructured {
	var found []*unstructured.Unstructured
	for _, object := range objects {
		if object.GetKind() == kind {
			found = append(found, object)
		}
	}
	return found
}

func convert[T any](t *testing.T, object *unstructured.Unstructured) T {
	t.Helper()
	var typed T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &typed); err != nil {
		t.Fatalf("convert %s/%s: %v", object.GetKind(), object.GetName(), err)
	}
	return typed
}

func only(t *testing.T, objects []*unstructured.Unstructured, kind string) *unstructured.Unstructured {
	t.Helper()
	found := ofKind(objects, kind)
	if len(found) != 1 {
		t.Fatalf("want exactly one %s, got %d", kind, len(found))
	}
	return found[0]
}

func deployment(t *testing.T, objects []*unstructured.Unstructured) appsv1.Deployment {
	t.Helper()
	return convert[appsv1.Deployment](t, only(t, objects, "Deployment"))
}

func env(t *testing.T, objects []*unstructured.Unstructured) map[string]string {
	t.Helper()
	variables := map[string]string{}
	for _, variable := range deployment(t, objects).Spec.Template.Spec.Containers[0].Env {
		variables[variable.Name] = variable.Value
	}
	return variables
}

func grants(rules []rbacv1.PolicyRule) map[string][]string {
	granted := map[string][]string{}
	for _, rule := range rules {
		for _, group := range rule.APIGroups {
			for _, resource := range rule.Resources {
				key := group + "/" + resource
				granted[key] = append(granted[key], rule.Verbs...)
			}
		}
	}
	for key, verbs := range granted {
		slices.Sort(verbs)
		granted[key] = slices.Compact(verbs)
	}
	return granted
}

func watchRBAC(objects []*unstructured.Unstructured, kind string) []*unstructured.Unstructured {
	var found []*unstructured.Unstructured
	for _, object := range ofKind(objects, kind) {
		if object.GetName() != leaderElection {
			found = append(found, object)
		}
	}
	return found
}

func namespaces(objects []*unstructured.Unstructured) []string {
	names := make([]string, 0, len(objects))
	for _, object := range objects {
		names = append(names, object.GetNamespace())
	}
	slices.Sort(names)
	return names
}

func wantVerbs(t *testing.T, granted map[string][]string, key string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if !slices.Equal(granted[key], want) {
		t.Errorf("%s verbs = %v, want %v", key, granted[key], want)
	}
}

func wantAtLeast(t *testing.T, granted map[string][]string, key string, want ...string) {
	t.Helper()
	for _, verb := range want {
		if !slices.Contains(granted[key], verb) {
			t.Errorf("%s verbs = %v, missing %s", key, granted[key], verb)
		}
	}
}

func clusterRole(t *testing.T, objects []*unstructured.Unstructured) rbacv1.ClusterRole {
	t.Helper()
	return convert[rbacv1.ClusterRole](t, only(t, objects, "ClusterRole"))
}

func namedRole(t *testing.T, objects []*unstructured.Unstructured, name string) *unstructured.Unstructured {
	t.Helper()
	for _, role := range ofKind(objects, "Role") {
		if role.GetName() == name {
			return role
		}
	}
	t.Fatalf("no Role named %s", name)
	return nil
}

func TestDefaultIsOneClusterRoleWithoutWildcards(t *testing.T) {
	objects := mustRender(t)
	role := clusterRole(t, objects)
	granted := grants(role.Rules)

	if len(watchRBAC(objects, "Role")) != 0 || len(watchRBAC(objects, "RoleBinding")) != 0 {
		t.Fatal("default render must not create Roles or RoleBindings for watched namespaces")
	}
	for _, rule := range role.Rules {
		for _, entries := range [][]string{rule.Verbs, rule.Resources, rule.APIGroups} {
			if slices.Contains(entries, "*") {
				t.Errorf("wildcard in rule %+v", rule)
			}
		}
	}
	for _, absent := range []string{"/namespaces", "networking.k8s.io/ingresses", "apiextensions.k8s.io/customresourcedefinitions"} {
		if _, found := granted[absent]; found {
			t.Errorf("unexpected grant on %s", absent)
		}
	}
	wantVerbs(t, granted, "/secrets", "get", "list", "watch", "create", "update", "patch", "delete")
	wantAtLeast(t, granted, "/serviceaccounts", "list", "watch")
	wantAtLeast(t, granted, "apps/statefulsets", "list", "watch")
	wantVerbs(t, granted, "qdrant.io/qdrantbackups", "get", "list", "watch", "patch", "create", "delete")
	wantVerbs(t, granted, "qdrant.io/qdrantclusters", "get", "list", "watch", "patch")
	wantVerbs(t, granted, "/persistentvolumes", "get")
	wantVerbs(t, granted, "/nodes", "list")
}

func TestEventsAreWritableThroughBothAPIGroups(t *testing.T) {
	objects := mustRender(t)
	cluster := grants(clusterRole(t, objects).Rules)
	leader := grants(convert[rbacv1.Role](t, namedRole(t, objects, leaderElection)).Rules)

	for name, rules := range map[string]map[string][]string{"cluster role": cluster, "leader election role": leader} {
		for _, key := range []string{"/events", "events.k8s.io/events"} {
			if !slices.Equal(rules[key], []string{"create", "patch"}) {
				t.Errorf("%s: %s verbs = %v, want create and patch", name, key, rules[key])
			}
		}
	}
}

func TestNoKopfAndNoCRDAccessAnywhere(t *testing.T) {
	cases := []struct {
		name string
		sets []string
	}{
		{"default", nil},
		{"watch namespaces", []string{"watchNamespaces={a,b}"}},
		{"ha", []string{"ha.enabled=true", "replicaCount=2"}},
		{"ha with watch namespaces", []string{"ha.enabled=true", "replicaCount=2", "watchNamespaces={a,b}"}},
	}
	legacy := []string{"LOG_LEVEL", "WATCH_NAMESPACES", "POD_ID", "PEERING_", "METRICS_PORT", "QDRANT_CHART_REPO"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objects := mustRender(t, tc.sets...)
			if crds := ofKind(objects, "CustomResourceDefinition"); len(crds) != 0 {
				t.Errorf("chart templates render %d CustomResourceDefinitions", len(crds))
			}
			for _, object := range objects {
				if strings.Contains(object.GetAPIVersion(), "kopf.dev") || strings.Contains(object.GetKind(), "Kopf") {
					t.Errorf("kopf object %s/%s", object.GetKind(), object.GetName())
				}
				var rules []rbacv1.PolicyRule
				switch object.GetKind() {
				case "ClusterRole":
					rules = convert[rbacv1.ClusterRole](t, object).Rules
				case "Role":
					rules = convert[rbacv1.Role](t, object).Rules
				}
				for _, rule := range rules {
					for _, group := range rule.APIGroups {
						if group == "kopf.dev" || group == "apiextensions.k8s.io" {
							t.Errorf("%s %s grants on %s", object.GetKind(), object.GetName(), group)
						}
					}
				}
			}
			for name := range env(t, objects) {
				for _, prefix := range legacy {
					if strings.HasPrefix(name, prefix) {
						t.Errorf("legacy variable %s", name)
					}
				}
			}
		})
	}
}

func TestWatchNamespacesMovesRulesIntoRoles(t *testing.T) {
	defaults := grants(clusterRole(t, mustRender(t)).Rules)
	objects := mustRender(t, "watchNamespaces={team-a,team-b}")
	roles := watchRBAC(objects, "Role")
	bindings := watchRBAC(objects, "RoleBinding")

	clusterScoped := map[string][]string{"/persistentvolumes": {"get"}, "/nodes": {"list"}}
	namespaced := map[string][]string{}
	for key, verbs := range defaults {
		if _, found := clusterScoped[key]; !found {
			namespaced[key] = verbs
		}
	}
	cluster := grants(clusterRole(t, objects).Rules)
	if fmt.Sprint(cluster) != fmt.Sprint(clusterScoped) {
		t.Errorf("cluster role grants %v, want %v", cluster, clusterScoped)
	}
	if got := namespaces(roles); !slices.Equal(got, []string{"team-a", "team-b"}) {
		t.Errorf("role namespaces = %v", got)
	}
	if got := namespaces(bindings); !slices.Equal(got, []string{"team-a", "team-b"}) {
		t.Errorf("binding namespaces = %v", got)
	}
	for _, object := range roles {
		granted := grants(convert[rbacv1.Role](t, object).Rules)
		if fmt.Sprint(granted) != fmt.Sprint(namespaced) {
			t.Errorf("role in %s grants %v, want %v", object.GetNamespace(), granted, namespaced)
		}
	}
	for _, object := range bindings {
		binding := convert[rbacv1.RoleBinding](t, object)
		if binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != object.GetName() {
			t.Errorf("binding roleRef = %+v", binding.RoleRef)
		}
		if len(binding.Subjects) != 1 || binding.Subjects[0].Namespace != releaseNamespace {
			t.Errorf("binding subjects = %+v", binding.Subjects)
		}
	}
}

func TestWatchNamespacesBecomeManagerNamespaces(t *testing.T) {
	cases := []struct {
		name string
		sets []string
		want string
	}{
		{"default watches everything", nil, ""},
		{"two namespaces", []string{"watchNamespaces={team-a,team-b}"}, "team-a,team-b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := env(t, mustRender(t, tc.sets...))["QDRANT_OPERATOR_MANAGER_NAMESPACES"]
			if !found || got != tc.want {
				t.Errorf("QDRANT_OPERATOR_MANAGER_NAMESPACES = %q (set %v), want %q", got, found, tc.want)
			}
		})
	}
}

func TestDefaultIsOneReplicaRecreate(t *testing.T) {
	objects := mustRender(t)
	spec := deployment(t, objects).Spec

	if spec.Replicas == nil || *spec.Replicas != 1 {
		t.Errorf("replicas = %v, want 1", spec.Replicas)
	}
	if spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || spec.Strategy.RollingUpdate != nil {
		t.Errorf("strategy = %+v, want Recreate", spec.Strategy)
	}
	if got := env(t, objects)["QDRANT_OPERATOR_MANAGER_LEADER_ELECTION"]; got != "false" {
		t.Errorf("LEADER_ELECTION = %q, want false", got)
	}
}

func TestSeveralReplicasWithoutHAAreRefused(t *testing.T) {
	_, err := render(t, releaseName, "replicaCount=2")
	if err == nil {
		t.Fatal("replicaCount=2 without ha.enabled rendered")
	}
	if !strings.Contains(err.Error(), "ha.enabled") {
		t.Errorf("error does not name ha.enabled: %v", err)
	}
}

func TestHARollsAndElectsALeader(t *testing.T) {
	objects := mustRender(t, "ha.enabled=true", "replicaCount=2")
	spec := deployment(t, objects).Spec

	if spec.Replicas == nil || *spec.Replicas != 2 {
		t.Errorf("replicas = %v, want 2", spec.Replicas)
	}
	if spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Fatalf("strategy = %+v, want RollingUpdate", spec.Strategy)
	}
	rolling := spec.Strategy.RollingUpdate
	if rolling == nil || rolling.MaxSurge.String() != "1" || rolling.MaxUnavailable.String() != "0" {
		t.Errorf("rollingUpdate = %+v, want maxSurge 1 maxUnavailable 0", rolling)
	}
	if got := env(t, objects)["QDRANT_OPERATOR_MANAGER_LEADER_ELECTION"]; got != "true" {
		t.Errorf("LEADER_ELECTION = %q, want true", got)
	}
}

func TestLeaseName(t *testing.T) {
	cases := []struct {
		name    string
		release string
		sets    []string
		want    string
	}{
		{"defaults to the release full name", "qdrant-operator", nil, "qdrant-operator"},
		{"full name carries the release name", "prod", nil, "prod-qdrant-operator"},
		{"follows fullnameOverride", "prod", []string{"fullnameOverride=ops"}, "ops"},
		{"follows ha.leaseName", "prod", []string{"ha.enabled=true", "ha.leaseName=custom-lease"}, "custom-lease"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objects, err := render(t, tc.release, tc.sets...)
			if err != nil {
				t.Fatal(err)
			}
			if got := env(t, objects)["QDRANT_OPERATOR_MANAGER_LEADER_ELECTION_ID"]; got != tc.want {
				t.Errorf("LEADER_ELECTION_ID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLeaseDurationFollowsTheValues(t *testing.T) {
	cases := []struct {
		name string
		sets []string
		want string
	}{
		{"default", nil, "15s"},
		{"custom", []string{"ha.enabled=true", "ha.leaseDuration=30s"}, "30s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := env(t, mustRender(t, tc.sets...))["QDRANT_OPERATOR_MANAGER_LEASE_DURATION"]; got != tc.want {
				t.Errorf("LEASE_DURATION = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLeaderElectionRoleLivesInTheReleaseNamespace(t *testing.T) {
	objects := mustRender(t, "watchNamespaces={team-a}")
	role := namedRole(t, objects, leaderElection)

	if role.GetNamespace() != releaseNamespace {
		t.Errorf("role namespace = %s, want %s", role.GetNamespace(), releaseNamespace)
	}
	wantVerbs(t, grants(convert[rbacv1.Role](t, role).Rules), "coordination.k8s.io/leases", "get", "create", "update")

	var bindings []*unstructured.Unstructured
	for _, object := range ofKind(objects, "RoleBinding") {
		if object.GetName() == leaderElection {
			bindings = append(bindings, object)
		}
	}
	if len(bindings) != 1 {
		t.Fatalf("want one leader election RoleBinding, got %d", len(bindings))
	}
	if bindings[0].GetNamespace() != releaseNamespace {
		t.Errorf("binding namespace = %s, want %s", bindings[0].GetNamespace(), releaseNamespace)
	}
	binding := convert[rbacv1.RoleBinding](t, bindings[0])
	if binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != leaderElection {
		t.Errorf("roleRef = %+v", binding.RoleRef)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Namespace != releaseNamespace {
		t.Errorf("subjects = %+v", binding.Subjects)
	}
}

func TestOperatorSettingsFollowTheValues(t *testing.T) {
	cases := []struct {
		name   string
		sets   []string
		want   map[string]string
		absent []string
	}{
		{
			name: "defaults",
			want: map[string]string{
				"QDRANT_OPERATOR_LOGGING_LEVEL":   "info",
				"QDRANT_OPERATOR_LOGGING_FORMAT":  "json",
				"QDRANT_OPERATOR_METRICS_ADDRESS": ":9090",
			},
			absent: []string{"QDRANT_OPERATOR_HELM_REPO"},
		},
		{
			name: "metrics disabled",
			sets: []string{"metrics.enabled=false"},
			want: map[string]string{"QDRANT_OPERATOR_METRICS_ADDRESS": "0"},
		},
		{
			name: "metrics port",
			sets: []string{"metrics.port=9191"},
			want: map[string]string{"QDRANT_OPERATOR_METRICS_ADDRESS": ":9191"},
		},
		{
			name: "chart repository",
			sets: []string{"operator.chartRepo=oci://registry.example.com/charts"},
			want: map[string]string{"QDRANT_OPERATOR_HELM_REPO": "oci://registry.example.com/charts"},
		},
		{
			name: "log level and format",
			sets: []string{"operator.logLevel=DEBUG", "operator.logFormat=text"},
			want: map[string]string{
				"QDRANT_OPERATOR_LOGGING_LEVEL":  "debug",
				"QDRANT_OPERATOR_LOGGING_FORMAT": "text",
			},
		},
		{
			name: "everything together",
			sets: []string{"watchNamespaces={a,b}", "ha.enabled=true", "replicaCount=2", "metrics.enabled=false"},
			want: map[string]string{
				"QDRANT_OPERATOR_MANAGER_NAMESPACES":      "a,b",
				"QDRANT_OPERATOR_MANAGER_LEADER_ELECTION": "true",
				"QDRANT_OPERATOR_METRICS_ADDRESS":         "0",
				"QDRANT_OPERATOR_LOGGING_LEVEL":           "info",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			variables := env(t, mustRender(t, tc.sets...))
			for name, want := range tc.want {
				if got := variables[name]; got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			for _, name := range tc.absent {
				if _, found := variables[name]; found {
					t.Errorf("%s is set", name)
				}
			}
		})
	}
}

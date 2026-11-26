//go:build kubernetes_e2e

// SPDX-License-Identifier: MIT
package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// racer runs race once, just before the first apply it passes on.
type racer struct {
	kubeapi.Applier
	race func()
	done bool
}

func (r *racer) Apply(ctx context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured, dryRun bool) (*unstructured.Unstructured, error) {
	if !r.done {
		r.done = true
		r.race()
	}
	return r.Applier.Apply(ctx, gvr, ns, obj, dryRun)
}

type admin struct{}

func (admin) IsAdmin(context.Context, string) bool { return true }

var (
	kc   *cluster
	adm  kubernetes.Interface
	rt   *tools.Runtime
	pols *policy.Store
	cred string
	key  string
	seq  int
)

func must[T any](v T, err error) T {
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return v
}

func meta(ns, name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }

func seed() {
	for _, ns := range []string{"team-a", "team-b"} {
		must(adm.CoreV1().Namespaces().Create(bg, &corev1.Namespace{ObjectMeta: meta("", ns)}, metav1.CreateOptions{}))
	}
	must(adm.CoreV1().Nodes().Create(bg, &corev1.Node{ObjectMeta: meta("", "node-1")}, metav1.CreateOptions{}))
	one := int32(2)
	labels := map[string]string{"app": "web"}
	tmpl := func(image string) corev1.PodTemplateSpec {
		return corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}}}
	}
	d := must(adm.AppsV1().Deployments("team-a").Create(bg, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web",
		Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"}},
		Spec: appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: tmpl("example/web:2")}}, metav1.CreateOptions{}))
	ctrl := true
	var rs2 *appsv1.ReplicaSet
	for rev, image := range map[string]string{"1": "example/web:1", "2": "example/web:2"} {
		t := tmpl(image)
		t.Labels = map[string]string{"app": "web", "pod-template-hash": "h" + rev}
		// The Deployment controller labels a replica set as its pod template.
		rs := must(adm.AppsV1().ReplicaSets("team-a").Create(bg, &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web-" + rev, Labels: t.Labels,
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": rev},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: d.UID, Controller: &ctrl}}},
			Spec: appsv1.ReplicaSetSpec{Selector: &metav1.LabelSelector{MatchLabels: t.Labels}, Template: t}}, metav1.CreateOptions{}))
		if rev == "2" {
			rs2 = rs
		}
	}
	p := must(adm.CoreV1().Pods("team-a").Create(bg, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web-1", Labels: labels,
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-2", UID: rs2.UID, Controller: &ctrl}}},
		Spec: corev1.PodSpec{NodeName: "node-1", Containers: []corev1.Container{{Name: "app", Image: "example/web:2",
			Env: []corev1.EnvVar{{Name: "API_TOKEN", Value: "inline-token-value"}}}}}}, metav1.CreateOptions{}))
	p.Status = corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}
	must(adm.CoreV1().Pods("team-a").UpdateStatus(bg, p, metav1.UpdateOptions{}))
	min := intstr.FromInt32(1)
	b := must(adm.PolicyV1().PodDisruptionBudgets("team-a").Create(bg, &policyv1.PodDisruptionBudget{ObjectMeta: meta("team-a", "web"),
		Spec: policyv1.PodDisruptionBudgetSpec{MinAvailable: &min, Selector: &metav1.LabelSelector{MatchLabels: labels}}}, metav1.CreateOptions{}))
	// No controller manager runs, so the budget's status is set by hand.
	b.Status = policyv1.PodDisruptionBudgetStatus{ObservedGeneration: b.Generation, DisruptionsAllowed: 0, CurrentHealthy: 1, DesiredHealthy: 1, ExpectedPods: 1}
	must(adm.PolicyV1().PodDisruptionBudgets("team-a").UpdateStatus(bg, b, metav1.UpdateOptions{}))
	must(adm.CoreV1().Secrets("team-a").Create(bg, &corev1.Secret{ObjectMeta: meta("team-a", "db"), StringData: map[string]string{"password": "secret-value"}}, metav1.CreateOptions{}))
	must(adm.AppsV1().Deployments("team-b").Create(bg, &appsv1.Deployment{ObjectMeta: meta("team-b", "api"),
		Spec: appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example/api:1"}}}}}}, metav1.CreateOptions{}))
	must(adm.CoreV1().Events("team-a").Create(bg, &corev1.Event{ObjectMeta: meta("team-a", "web.1"), Reason: "ScalingReplicaSet", Message: "Scaled up",
		Type: "Normal", InvolvedObject: corev1.ObjectReference{Kind: "Deployment", Namespace: "team-a", Name: "web", UID: d.UID}}, metav1.CreateOptions{}))
	must(adm.RbacV1().Roles("team-a").Create(bg, &rbacv1.Role{ObjectMeta: meta("team-a", "edit"),
		Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list", "watch", "patch", "update"}, APIGroups: []string{"", "apps"}, Resources: []string{"pods", "deployments"}}}}, metav1.CreateOptions{}))
	must(adm.RbacV1().RoleBindings("team-a").Create(bg, &rbacv1.RoleBinding{ObjectMeta: meta("team-a", "edit"),
		RoleRef:  rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "edit"},
		Subjects: []rbacv1.Subject{{Kind: "User", Name: "e2e-team", APIGroup: "rbac.authorization.k8s.io"}}}, metav1.CreateOptions{}))
}

var _ = BeforeSuite(func() {
	kc = startCluster()
	adm = kc.admin()
	seed()
	cred = kc.kubeconfig(adminToken)
	Expect(len(cred)).To(BeNumerically("<=", contract.MaxCredentialBytes))
	key = kc.server + "#context=e2e"
	pols = policy.Open(GinkgoT().TempDir())
	// Out of the box a cluster may not be changed. The specs of the write
	// tools run as if an administrator had allowed every write tool but exec.
	_, err := pols.SetDefault(writable)
	Expect(err).NotTo(HaveOccurred())
	// The API server listens on the loopback interface, which the add-on
	// refuses to dial. The suite uses the factory that allows it, which only
	// the kubernetes_e2e build tag builds.
	rt = tools.New(kubeapi.NewLoopbackFactory(), pols).WithDataDir(GinkgoT().TempDir())
	rt.Admin = admin{}
})

var _ = ReportAfterEach(func(r SpecReport) {
	if r.Failed() && kc != nil {
		kc.printLogs()
	}
})

// secrets lists what must never appear in an answer.
var secrets = []string{adminToken, teamToken, "secret-value", "c2VjcmV0LXZhbHVl", "inline-token-value"}

func noLeak(b string) {
	for _, s := range secrets {
		ExpectWithOffset(2, b).NotTo(ContainSubstring(s))
	}
}

func env(credential, tool string, args map[string]any) contract.Envelope {
	if args == nil {
		args = map[string]any{}
	}
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	seq++
	return contract.Envelope{Request: contract.Request{AddonID: "kubernetes", OperationID: fmt.Sprintf("op-%d", seq), Arguments: raw, Credential: credential, ResourceKey: key}}
}

func read(tool string, args map[string]any) (map[string]any, error) {
	return readAs(cred, tool, args)
}

func readAs(credential, tool string, args map[string]any) (map[string]any, error) {
	res, err := rt.Read(bg, env(credential, tool, args))
	if err != nil {
		noLeak(err.Error())
		return nil, err
	}
	noLeak(string(res.Payload))
	var out struct {
		Data map[string]any `json:"data"`
	}
	Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
	return out.Data, nil
}

func mustRead(tool string, args map[string]any) map[string]any {
	d, err := read(tool, args)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return d
}

// write runs the host's sequence: a preview, then an apply with the
// preview's revision. A confirmed call and one the person lets run without
// a card take the same sequence in the add-on.
func write(tool string, args map[string]any) (contract.Preview, contract.Receipt, map[string]any, error) {
	e := env(cred, tool, args)
	p, err := rt.Preview(bg, e)
	if err != nil {
		noLeak(err.Error())
		return p, contract.Receipt{}, nil, err
	}
	noLeak(string(p.Summary))
	rc, err := rt.Apply(bg, e, p.Revision)
	if err != nil {
		noLeak(err.Error())
		return p, rc, nil, err
	}
	noLeak(string(rc.Result.Payload))
	var out struct {
		Data map[string]any `json:"data"`
	}
	Expect(json.Unmarshal(rc.Result.Payload, &out)).To(Succeed())
	return p, rc, out.Data, nil
}

func sample(p contract.Preview) map[string]any {
	var s struct {
		Sample map[string]any `json:"sample"`
	}
	Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
	return s.Sample
}

// writable allows every namespace and every write tool but exec.
var writable = policy.Policy{Namespaces: []string{policy.All}, WriteTools: []string{"scale", "rollout-restart", "rollout-undo", "delete-pod", "cordon", "uncordon", "drain", "apply", "patch"}}

func setPolicy(p policy.Policy) {
	_, err := pols.Set(key, p)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _, _ = pols.Set(key, writable) })
}

var _ = Describe("the tools against a real API server", Ordered, func() {
	It("check reports v1.37 and the team rules", func() {
		d := mustRead(tools.ToolCheck, nil)
		chk := d["check"].(map[string]any)
		Expect(chk["version"]).To(HavePrefix("v1.37"))
		d, err := readAs(kc.kubeconfig(teamToken), tools.ToolCheck, map[string]any{"namespace": "team-a"})
		Expect(err).NotTo(HaveOccurred())
		rules, _ := json.Marshal(d["check"].(map[string]any)["rules"])
		Expect(string(rules)).To(ContainSubstring("deployments"))
	})

	It("list, get, describe, events and api-resources answer from the real server", func() {
		rows := mustRead(tools.ToolList, map[string]any{"kind": "pods", "namespace": "team-a"})["rows"].([]any)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].(map[string]any)["name"]).To(Equal("web-1"))
		Expect(mustRead(tools.ToolGet, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web"})["yaml"]).To(ContainSubstring("example/web:2"))
		d := mustRead(tools.ToolDescribe, map[string]any{"kind": "deployments.apps", "namespace": "team-a", "name": "web"})
		Expect(d["related"]).To(ContainElement(ContainSubstring("ReplicaSet/team-a/web-1")))
		Expect(d["events"]).To(HaveLen(1))
		Expect(mustRead(tools.ToolEvents, map[string]any{"namespace": "team-a"})["count"]).To(BeNumerically("==", 1))
		Expect(mustRead(tools.ToolAPIResources, map[string]any{"group": "apps"})["count"]).To(BeNumerically(">=", 4))
		Expect(mustRead(tools.ToolList, map[string]any{"kind": "nodes"})["rows"]).To(HaveLen(1))
	})

	It("get of the Secret shows keys and byte lengths only", func() {
		y := mustRead(tools.ToolGet, map[string]any{"kind": "v1/Secret", "namespace": "team-a", "name": "db"})["yaml"]
		Expect(y).To(ContainSubstring("password: <12 bytes>"))
	})

	It("scale previews and applies; the live replicas change only after Apply", func() {
		e := env(cred, tools.ToolScale, map[string]any{"kind": "Deployment", "namespace": "team-a", "name": "web", "replicas": 3})
		p, err := rt.Preview(bg, e)
		Expect(err).NotTo(HaveOccurred())
		Expect(sample(p)["from"]).To(BeNumerically("==", 2))
		Expect(*must(adm.AppsV1().Deployments("team-a").Get(bg, "web", metav1.GetOptions{})).Spec.Replicas).To(BeNumerically("==", 2))
		rc, err := rt.Apply(bg, e, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(*must(adm.AppsV1().Deployments("team-a").Get(bg, "web", metav1.GetOptions{})).Spec.Replicas).To(BeNumerically("==", 3))
	})

	It("rollout restart sets restartedAt; rollout undo goes to revision 1", func() {
		_, _, _, err := write(tools.ToolRolloutRestart, map[string]any{"kind": "Deployment", "namespace": "team-a", "name": "web"})
		Expect(err).NotTo(HaveOccurred())
		d := must(adm.AppsV1().Deployments("team-a").Get(bg, "web", metav1.GetOptions{}))
		Expect(d.Spec.Template.Annotations).To(HaveKey("kubectl.kubernetes.io/restartedAt"))
		p, _, _, err := write(tools.ToolRolloutUndo, map[string]any{"namespace": "team-a", "name": "web"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sample(p)["to"]).To(BeNumerically("==", 1))
		d = must(adm.AppsV1().Deployments("team-a").Get(bg, "web", metav1.GetOptions{}))
		Expect(d.Spec.Template.Spec.Containers[0].Image).To(Equal("example/web:1"))
	})

	It("delete-pod deletes", func() {
		must(adm.CoreV1().Pods("team-a").Create(bg, &corev1.Pod{ObjectMeta: meta("team-a", "tmp"),
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example/tmp:1"}}}}, metav1.CreateOptions{}))
		_, rc, _, err := write(tools.ToolDeletePod, map[string]any{"namespace": "team-a", "name": "tmp"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		_, err = adm.CoreV1().Pods("team-a").Get(bg, "tmp", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("cordon and uncordon flip unschedulable", func() {
		_, _, _, err := write(tools.ToolCordon, map[string]any{"node": "node-1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(must(adm.CoreV1().Nodes().Get(bg, "node-1", metav1.GetOptions{})).Spec.Unschedulable).To(BeTrue())
		_, _, _, err = write(tools.ToolUncordon, map[string]any{"node": "node-1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(must(adm.CoreV1().Nodes().Get(bg, "node-1", metav1.GetOptions{})).Spec.Unschedulable).To(BeFalse())
	})

	It("drain is blocked by the PDB and reports it", func() {
		_, _, data, err := write(tools.ToolDrain, map[string]any{"node": "node-1", "timeoutSeconds": 2})
		Expect(err).NotTo(HaveOccurred())
		Expect(data["done"]).To(BeFalse())
		blocked := data["blocked"].([]any)
		Expect(blocked).To(HaveLen(1))
		Expect(blocked[0].(map[string]any)["pod"]).To(Equal("team-a/web-1"))
		Expect(blocked[0].(map[string]any)["reason"]).To(ContainSubstring("PodDisruptionBudget"))
		Expect(must(adm.CoreV1().Nodes().Get(bg, "node-1", metav1.GetOptions{})).Spec.Unschedulable).To(BeTrue())
		must(adm.CoreV1().Pods("team-a").Get(bg, "web-1", metav1.GetOptions{}))
	})

	It("apply previews a real dry-run diff and writes only on Apply", func() {
		manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: team-a\ndata:\n  mode: fast\n"
		e := env(cred, tools.ToolApply, map[string]any{"manifest": manifest})
		p, err := rt.Preview(bg, e)
		Expect(err).NotTo(HaveOccurred())
		Expect(sample(p)["change"]).To(Equal("create"))
		Expect(sample(p)["diff"]).To(ContainSubstring("+  mode: fast"))
		_, err = adm.CoreV1().ConfigMaps("team-a").Get(bg, "settings", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the dry run stored nothing")
		rc, err := rt.Apply(bg, e, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		cm := must(adm.CoreV1().ConfigMaps("team-a").Get(bg, "settings", metav1.GetOptions{}))
		Expect(cm.Data).To(HaveKeyWithValue("mode", "fast"))
		Expect(cm.ManagedFields[0].Manager).To(Equal("localai-kubernetes"))

		p, _, _, err = write(tools.ToolApply, map[string]any{"manifest": strings.Replace(manifest, "fast", "slow", 1)})
		Expect(err).NotTo(HaveOccurred())
		Expect(sample(p)["diff"]).To(ContainSubstring("-  mode: fast"))
		Expect(sample(p)["diff"]).To(ContainSubstring("+  mode: slow"))
	})

	It("server-side apply: a conflict with another field manager comes back, nothing is forced", func() {
		cm := corev1ac.ConfigMap("shared", "team-a").WithData(map[string]string{"owner": "other"})
		must(adm.CoreV1().ConfigMaps("team-a").Apply(bg, cm, metav1.ApplyOptions{FieldManager: "other-team", Force: true}))
		manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: shared\n  namespace: team-a\ndata:\n  owner: localai\n"
		_, err := rt.Preview(bg, env(cred, tools.ToolApply, map[string]any{"manifest": manifest}))
		Expect(err).To(MatchError(ContainSubstring("409 Conflict")))
		Expect(err.Error()).To(ContainSubstring("other-team"))
		Expect(must(adm.CoreV1().ConfigMaps("team-a").Get(bg, "shared", metav1.GetOptions{})).Data).To(HaveKeyWithValue("owner", "other"))
	})

	It("server-side apply: a field the add-on applied before and leaves out is removed", func() {
		two := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: owned\n  namespace: team-a\ndata:\n  keep: a\n  drop: b\n"
		_, _, _, err := write(tools.ToolApply, map[string]any{"manifest": two})
		Expect(err).NotTo(HaveOccurred())
		one := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: owned\n  namespace: team-a\ndata:\n  keep: a\n"
		p, _, _, err := write(tools.ToolApply, map[string]any{"manifest": one})
		Expect(err).NotTo(HaveOccurred())
		Expect(sample(p)["diff"]).To(ContainSubstring("-  drop: b"))
		Expect(must(adm.CoreV1().ConfigMaps("team-a").Get(bg, "owned", metav1.GetOptions{})).Data).To(Equal(map[string]string{"keep": "a"}))
	})

	It("a json patch that copies a Secret value is refused while values are hidden", func() {
		_, _, _, err := write(tools.ToolPatch, map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "json",
			"patch": `[{"op":"copy","from":"/data/password","path":"/metadata/annotations/leak"}]`})
		Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")))
		Expect(must(adm.CoreV1().Secrets("team-a").Get(bg, "db", metav1.GetOptions{})).Annotations).NotTo(HaveKey("leak"))
	})

	It("an apply that guesses a hidden Secret value owned by another field manager is refused before the dry run", func() {
		sec := corev1ac.Secret("guarded", "team-a").WithStringData(map[string]string{"password": "secret-value"})
		must(adm.CoreV1().Secrets("team-a").Apply(bg, sec, metav1.ApplyOptions{FieldManager: "other-team", Force: true}))
		for _, guess := range []string{"secret-value", "wrong"} {
			manifest := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: guarded\n  namespace: team-a\nstringData:\n  password: " + guess + "\n"
			_, err := rt.Preview(bg, env(cred, tools.ToolApply, map[string]any{"manifest": manifest}))
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), "a right and a wrong guess get the same answer")
			Expect(err.Error()).NotTo(ContainSubstring("409"))
		}
		_, _, _, err := write(tools.ToolApply, map[string]any{"manifest": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: guarded\n  namespace: team-a\nstringData:\n  other: x\n"})
		Expect(err).NotTo(HaveOccurred(), "a new key can still be applied")
		got := must(adm.CoreV1().Secrets("team-a").Get(bg, "guarded", metav1.GetOptions{}))
		Expect(string(got.Data["password"])).To(Equal("secret-value"))
	})

	It("an apply of a hidden Secret is pinned to the version it checked: a change in between is a conflict whatever the guess", func() {
		for i, guess := range []string{"c2VjcmV0LXZhbHVl", "d3Jvbmc="} {
			name := fmt.Sprintf("raced-%d", i)
			must(adm.CoreV1().Secrets("team-a").Create(bg, &corev1.Secret{ObjectMeta: meta("team-a", name), StringData: map[string]string{"user": "app"}}, metav1.CreateOptions{}))
			// Another writer sets the value after the add-on checked the
			// live Secret and before its dry run.
			raced := tools.New(func(cfg *rest.Config) (*kubeapi.Client, error) {
				c, err := kubeapi.NewLoopbackFactory()(cfg)
				if err != nil {
					return nil, err
				}
				c.Writer = &racer{Applier: c.Writer, race: func() {
					sec := corev1ac.Secret(name, "team-a").WithData(map[string][]byte{"password": []byte("secret-value")})
					must(adm.CoreV1().Secrets("team-a").Apply(bg, sec, metav1.ApplyOptions{FieldManager: "other-team", Force: true}))
				}}
				return c, nil
			}, pols).WithDataDir(GinkgoT().TempDir())
			manifest := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: " + name + "\n  namespace: team-a\ndata:\n  password: " + guess + "\n"
			_, err := raced.Preview(bg, env(cred, tools.ToolApply, map[string]any{"manifest": manifest}))
			Expect(err).To(MatchError(ContainSubstring("409")), "guess %q: a right and a wrong guess get the same answer", guess)
			noLeak(err.Error())
		}
	})

	It("a json patch that tests an env value of a Deployment is refused while values are hidden", func() {
		_, _, _, err := write(tools.ToolPatch, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "json",
			"patch": `[{"op":"test","path":"/spec/template/spec/containers/0/env/0/value","value":"x"}]`})
		Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")))
	})

	It("patch merge previews and applies", func() {
		p, _, data, err := write(tools.ToolPatch, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "merge",
			"patch": `{"metadata":{"labels":{"tier":"front"}}}`})
		Expect(err).NotTo(HaveOccurred())
		Expect(sample(p)["diff"]).To(ContainSubstring("+    tier: front"))
		Expect(data["patched"]).To(Equal("Deployment/team-a/web"))
		Expect(must(adm.AppsV1().Deployments("team-a").Get(bg, "web", metav1.GetOptions{})).Labels).To(HaveKeyWithValue("tier", "front"))
	})

	It("apply of a Namespace is refused by policy", func() {
		_, _, _, err := write(tools.ToolApply, map[string]any{"manifest": "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: evil\n"})
		Expect(err).To(MatchError(ContainSubstring("(clusterScoped)")))
		_, err = adm.CoreV1().Namespaces().Get(bg, "evil", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("a team-b write is refused with namespaces [team-a] for preview and for apply", func() {
		args := map[string]any{"kind": "Deployment", "namespace": "team-b", "name": "api", "replicas": 0}
		e := env(cred, tools.ToolScale, args)
		p, err := rt.Preview(bg, e)
		Expect(err).NotTo(HaveOccurred())
		setPolicy(policy.Policy{Namespaces: []string{"team-a"}, WriteTools: writable.WriteTools})
		_, err = rt.Apply(bg, e, p.Revision)
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
		_, _, _, err = write(tools.ToolScale, args)
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
		Expect(*must(adm.AppsV1().Deployments("team-b").Get(bg, "api", metav1.GetOptions{})).Spec.Replicas).To(BeNumerically("==", 2))
	})

	It("a disabled tool is refused", func() {
		setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: []string{"scale"}})
		_, _, _, err := write(tools.ToolCordon, map[string]any{"node": "node-1"})
		Expect(err).To(MatchError(ContainSubstring("(writeTools)")))
	})

	It("exec is refused by the default policy", func() {
		_, _, _, err := write(tools.ToolExec, map[string]any{"namespace": "team-a", "pod": "web-1", "command": []string{"ls"}})
		Expect(err).To(MatchError(ContainSubstring("(writeTools): exec is disabled")))
	})

	It("with the team credential, a team-b read passes the API 403 through", func() {
		_, err := readAs(kc.kubeconfig(teamToken), tools.ToolList, map[string]any{"kind": "pods", "namespace": "team-b"})
		var up *contract.UpstreamError
		Expect(errors.As(err, &up)).To(BeTrue(), fmt.Sprint(err))
		Expect(up.Status).To(Equal(403))
		Expect(err.Error()).To(ContainSubstring("403 Forbidden"))
	})

	It("the add-on's own factory refuses an API server on the loopback interface, named localhost", func() {
		own := tools.New(kubeapi.NewFactory(), pols)
		_, err := own.Read(bg, env(cred, tools.ToolCheck, nil))
		Expect(err).To(MatchError(ContainSubstring("loopback")))
		noLeak(err.Error())
	})

	It("the credential never appears in any result", func() {
		// Every helper above checks each payload and error; this spec runs
		// the error paths once more with a wrong context and a wrong server.
		for _, k := range []string{kc.server + "#context=nope", "https://127.0.0.2:1#context=e2e"} {
			e := env(cred, tools.ToolCheck, nil)
			e.Request.ResourceKey = k
			_, err := rt.Read(bg, e)
			Expect(err).To(HaveOccurred())
			noLeak(err.Error())
		}
	})
})

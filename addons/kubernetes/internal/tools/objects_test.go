// SPDX-License-Identifier: MIT
package tools_test

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func meta(ns, name string, age time.Duration) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(ns + "-" + name), ResourceVersion: "1",
		CreationTimestamp: metav1.NewTime(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC).Add(-age))}
}

func pod(ns, name, node string, age time.Duration) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: meta(ns, name, age),
		Spec:   corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "app", Image: "example/app:1"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "app", Ready: true, RestartCount: 2}}}}
	return p
}

func owned(p *corev1.Pod, kind, name string) *corev1.Pod {
	ctrl := true
	p.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: kind, Name: name, UID: types.UID(p.Namespace + "-" + name), Controller: &ctrl}}
	return p
}

func deployment(ns, name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: meta(ns, name, 48*time.Hour),
		Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example/app:2",
					Env: []corev1.EnvVar{{Name: "API_TOKEN", Value: "inline-token-value"}, {Name: "MODE", Value: "prod"},
						{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "db"}, Key: "password"}}}}}}}}},
		Status: appsv1.DeploymentStatus{ReadyReplicas: replicas, UpdatedReplicas: replicas, AvailableReplicas: replicas}}
}

func replicaSet(d *appsv1.Deployment, name, revision, image string) *appsv1.ReplicaSet {
	ctrl := true
	rs := &appsv1.ReplicaSet{ObjectMeta: meta(d.Namespace, name, 24*time.Hour),
		Spec: appsv1.ReplicaSetSpec{Template: *d.Spec.Template.DeepCopy()}}
	rs.Annotations = map[string]string{"deployment.kubernetes.io/revision": revision}
	rs.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: d.Name, UID: d.UID, Controller: &ctrl}}
	rs.Spec.Template.Labels = map[string]string{"app": d.Name, "pod-template-hash": name}
	// The Deployment controller labels a replica set as its pod template.
	rs.Labels = map[string]string{"app": d.Name, "pod-template-hash": name}
	rs.Spec.Template.Spec.Containers[0].Image = image
	return rs
}

func node(name string) *corev1.Node {
	n := &corev1.Node{ObjectMeta: meta("", name, 72*time.Hour)}
	n.Labels = map[string]string{"node-role.kubernetes.io/worker": ""}
	n.Status.NodeInfo.KubeletVersion = "v1.37.0"
	return n
}

func secret(ns, name string) *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: meta(ns, name, time.Hour), Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"password": []byte("secret-value"), "user": []byte("app")}}
	s.Annotations = map[string]string{"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"password":"c2VjcmV0LXZhbHVl"}}`}
	return s
}

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: meta("", name, 100*time.Hour)}
}

func event(ns, name string, obj metav1.Object, kind, reason string, last time.Duration) *corev1.Event {
	return &corev1.Event{ObjectMeta: meta(ns, name, last),
		InvolvedObject: corev1.ObjectReference{Kind: kind, Namespace: obj.GetNamespace(), Name: obj.GetName(), UID: obj.GetUID()},
		Reason:         reason, Message: reason + " happened", Type: "Normal", Count: 1,
		LastTimestamp: metav1.NewTime(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC).Add(-last))}
}

func widget(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "example.com/v1", "kind": "Widget",
		"metadata": map[string]any{"name": name, "namespace": ns, "uid": "w-1"}, "spec": map[string]any{"size": int64(1)}}}
}

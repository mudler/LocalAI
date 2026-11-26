// SPDX-License-Identifier: MIT
package kubeapi

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Target is a kind the cluster serves, resolved from what a model wrote.
type Target struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
}

var versionShape = regexp.MustCompile(`^v[0-9]+((alpha|beta)[0-9]+)?$`)

// Resolve turns a kind as a person writes it into the resource the cluster
// serves: pods, Pod, pod, po, deployments.apps, Deployment.apps,
// apps/v1/Deployment, v1/Secret, secrets.v1. or the plural of a custom
// resource.
func Resolve(m meta.RESTMapper, kind string) (Target, error) {
	s := strings.TrimSpace(kind)
	if s == "" || len(s) > 100 {
		return Target{}, fmt.Errorf("name a kind, for example pods or deployments.apps")
	}
	var mapping *meta.RESTMapping
	var err error
	switch parts := strings.Split(s, "/"); len(parts) {
	case 3:
		g := parts[0]
		if g == "core" {
			g = ""
		}
		mapping, err = m.RESTMapping(schema.GroupKind{Group: g, Kind: parts[2]}, parts[1])
	case 2:
		if versionShape.MatchString(parts[0]) {
			mapping, err = m.RESTMapping(schema.GroupKind{Kind: parts[1]}, parts[0])
		} else {
			mapping, err = m.RESTMapping(schema.GroupKind{Group: parts[0], Kind: parts[1]})
		}
	case 1:
		mapping, err = resolveName(m, s)
	default:
		err = fmt.Errorf("too many slashes")
	}
	if err != nil || mapping == nil {
		return Target{}, fmt.Errorf("the cluster serves no kind %q: use api-resources to see the kinds it serves", short(s))
	}
	return Target{GVR: mapping.Resource, Kind: mapping.GroupVersionKind.Kind, Namespaced: mapping.Scope.Name() == meta.RESTScopeNameNamespace}, nil
}

func resolveName(m meta.RESTMapper, s string) (*meta.RESTMapping, error) {
	gvr, gr := schema.ParseResourceArg(s)
	var gvk schema.GroupVersionKind
	var err error
	if gvr != nil {
		gvk, err = m.KindFor(*gvr)
	}
	if gvr == nil || err != nil {
		gvk, err = m.KindFor(gr.WithVersion(""))
	}
	if err == nil {
		return m.RESTMapping(gvk.GroupKind(), gvk.Version)
	}
	// A kind with a group, such as Deployment.apps.
	if full, gk := schema.ParseKindArg(s); full != nil {
		if mp, err := m.RESTMapping(full.GroupKind(), full.Version); err == nil {
			return mp, nil
		}
	} else {
		return m.RESTMapping(gk)
	}
	_, gk := schema.ParseKindArg(s)
	return m.RESTMapping(gk)
}

func short(s string) string {
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

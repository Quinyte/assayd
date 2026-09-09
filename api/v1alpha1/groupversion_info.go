// Package v1alpha1 contains the assayd platform API.
//
// Group is assayd.dev — a codename per ADR-0001; renaming before public release
// is a tracked task and is a mechanical regenerate, not a redesign.
// +kubebuilder:object:generate=true
// +groupName=assayd.dev
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the group and version for this API.
	GroupVersion = schema.GroupVersion{Group: "assayd.dev", Version: "v1alpha1"}

	// SchemeBuilder registers the Go types with a scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

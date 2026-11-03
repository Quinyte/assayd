// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The API server refuses a zoned spec.clusterIP, on create and on update, so
// no stored Service reaches evalclient.Check's request refusal, which only a
// zone a URL cannot carry in a host reaches (design 16 §1.1 step 3's #143
// residue, A27.3). This row measures Kubernetes, not assayd: it pins the
// fact the residue rests on, on every kube-apiserver version CI's envtest
// uses. A refusal is counted only when its cause names the ClusterIP and says
// it is not a valid IP address, so a Service refused for another field does
// not pass it, and neither does one refused for its family: envtest's API
// server is IPv4 only, and refuses an unzoned fe80::1 as "IPv6 is not
// configured on this cluster", a cause this row does not accept.
//
// Mutations (A27.3): create the Service with an in-range address unzoned, or
// update it to its own address unzoned; either is admitted, and the row fails.
func TestTheAPIServerRefusesAZonedClusterIP(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()
	service := func(name, ip string) *corev1.Service {
		s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}}}
		if ip != "" {
			s.Spec.ClusterIP, s.Spec.ClusterIPs = ip, []string{ip}
		}
		return s
	}
	refusedForTheClusterIP := func(t *testing.T, what string, err error) {
		t.Helper()
		if !apierrors.IsInvalid(err) {
			t.Fatalf("%s: %v; want Invalid", what, err)
		}
		var status apierrors.APIStatus
		if !errors.As(err, &status) || status.Status().Details == nil {
			t.Fatalf("%s: %v carries no causes", what, err)
		}
		for _, c := range status.Status().Details.Causes {
			if strings.HasPrefix(c.Field, "spec.clusterIP") && strings.Contains(c.Message, "valid IP address") {
				return
			}
		}
		t.Fatalf("%s: %v; want a cause on spec.clusterIP(s) saying it is not a valid IP address", what, err)
	}

	for _, ip := range []string{"fe80::1%eth0", "fe80::1%a/b"} {
		err := k8s.Create(ctx, service("zoned-"+strings.NewReplacer(":", "", "%", "-", "/", "-", ".", "-").Replace(ip), ip))
		refusedForTheClusterIP(t, "create with spec.clusterIP "+ip, err)
	}

	// On update: an admitted Service whose ClusterIP is its own address plus
	// a zone.
	s := service("unzoned", "")
	if err := k8s.Create(ctx, s); err != nil {
		t.Fatalf("create an unzoned Service: %v", err)
	}
	if s.Spec.ClusterIP == "" {
		t.Fatal("the API server allocated no ClusterIP")
	}
	zoned := s.Spec.ClusterIP + "%eth0"
	s.Spec.ClusterIP, s.Spec.ClusterIPs = zoned, []string{zoned}
	refusedForTheClusterIP(t, "update spec.clusterIP to "+zoned, k8s.Update(ctx, s))
}

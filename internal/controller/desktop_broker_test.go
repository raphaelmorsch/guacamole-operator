/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

func TestSortBrokerQueuePriorityThenFIFO(t *testing.T) {
	p10 := int32(10)
	p5 := int32(5)
	t0 := metav1.NewTime(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	t1 := metav1.NewTime(time.Date(2026, 8, 1, 12, 1, 0, 0, time.UTC))
	t2 := metav1.NewTime(time.Date(2026, 8, 1, 12, 2, 0, 0, time.UTC))

	sessions := []guacamolev1alpha1.DesktopSession{
		{ObjectMeta: metav1.ObjectMeta{Name: "c", CreationTimestamp: t2}, Spec: guacamolev1alpha1.DesktopSessionSpec{Priority: &p5}},
		{ObjectMeta: metav1.ObjectMeta{Name: "a", CreationTimestamp: t1}, Spec: guacamolev1alpha1.DesktopSessionSpec{Priority: &p10}},
		{ObjectMeta: metav1.ObjectMeta{Name: "b", CreationTimestamp: t0}, Spec: guacamolev1alpha1.DesktopSessionSpec{Priority: &p10}},
	}
	sortBrokerQueue(sessions)
	if sessions[0].Name != "b" || sessions[1].Name != "a" || sessions[2].Name != "c" {
		t.Fatalf("unexpected order: %s,%s,%s", sessions[0].Name, sessions[1].Name, sessions[2].Name)
	}
}

func TestBrokerQueuePosition(t *testing.T) {
	p1 := int32(1)
	waiters := []guacamolev1alpha1.DesktopSession{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "ns", CreationTimestamp: metav1.Now()},
			Spec:       guacamolev1alpha1.DesktopSessionSpec{Priority: &p1},
			Status:     guacamolev1alpha1.DesktopSessionStatus{Phase: guacamolev1alpha1.DesktopSessionPhaseQueued},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "bob", Namespace: "ns", CreationTimestamp: metav1.Now()},
			Status:     guacamolev1alpha1.DesktopSessionStatus{Phase: guacamolev1alpha1.DesktopSessionPhasePending},
		},
	}
	pos, length := brokerQueuePosition(&waiters[1], waiters)
	if pos != 2 || length != 2 {
		t.Fatalf("bob expected position 2/2, got %d/%d", pos, length)
	}
	if !isBrokerQueueHead(&waiters[0], waiters) {
		t.Fatal("alice should be queue head")
	}
}

func TestBrokerQueueTimedOut(t *testing.T) {
	max := int64(30)
	queuedAt := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	session := &guacamolev1alpha1.DesktopSession{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute))},
		Spec:       guacamolev1alpha1.DesktopSessionSpec{MaxQueueSeconds: &max},
		Status:     guacamolev1alpha1.DesktopSessionStatus{QueuedAt: &queuedAt},
	}
	if !brokerQueueTimedOut(session, time.Now()) {
		t.Fatal("expected timeout")
	}
}

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
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

// isWaitingForBroker reports whether the session still needs a desktop allocation
// and should participate in the pool's fair queue.
func isWaitingForBroker(session *guacamolev1alpha1.DesktopSession) bool {
	if session == nil || !session.DeletionTimestamp.IsZero() {
		return false
	}
	if session.Status.DesktopName != "" {
		return false
	}
	switch session.Status.Phase {
	case guacamolev1alpha1.DesktopSessionPhaseFailed,
		guacamolev1alpha1.DesktopSessionPhaseReleased,
		guacamolev1alpha1.DesktopSessionPhaseReady:
		return false
	default:
		return true
	}
}

func sessionPriority(session *guacamolev1alpha1.DesktopSession) int32 {
	if session.Spec.Priority != nil {
		return *session.Spec.Priority
	}
	return 0
}

// sortBrokerQueue orders waiting sessions: higher priority first, then FIFO.
func sortBrokerQueue(sessions []guacamolev1alpha1.DesktopSession) {
	sort.SliceStable(sessions, func(i, j int) bool {
		pi, pj := sessionPriority(&sessions[i]), sessionPriority(&sessions[j])
		if pi != pj {
			return pi > pj
		}
		ti := sessions[i].CreationTimestamp.Time
		tj := sessions[j].CreationTimestamp.Time
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return sessions[i].Name < sessions[j].Name
	})
}

// brokerQueuePosition returns 1-based position and queue length for session among waiters.
// Returns 0,0 when the session is not waiting.
func brokerQueuePosition(session *guacamolev1alpha1.DesktopSession, waiters []guacamolev1alpha1.DesktopSession) (position, length int32) {
	if !isWaitingForBroker(session) {
		return 0, 0
	}
	ordered := append([]guacamolev1alpha1.DesktopSession(nil), waiters...)
	sortBrokerQueue(ordered)
	length = int32(len(ordered))
	for i := range ordered {
		if ordered[i].Name == session.Name && ordered[i].Namespace == session.Namespace {
			return int32(i + 1), length
		}
	}
	return 0, length
}

func isBrokerQueueHead(session *guacamolev1alpha1.DesktopSession, waiters []guacamolev1alpha1.DesktopSession) bool {
	pos, _ := brokerQueuePosition(session, waiters)
	return pos == 1
}

func brokerQueueTimedOut(session *guacamolev1alpha1.DesktopSession, now time.Time) bool {
	if session.Spec.MaxQueueSeconds == nil || *session.Spec.MaxQueueSeconds <= 0 {
		return false
	}
	start := session.CreationTimestamp.Time
	if session.Status.QueuedAt != nil && !session.Status.QueuedAt.IsZero() {
		start = session.Status.QueuedAt.Time
	}
	deadline := start.Add(time.Duration(*session.Spec.MaxQueueSeconds) * time.Second)
	return !now.Before(deadline)
}

func clearBrokerQueueStatus(status *guacamolev1alpha1.DesktopSessionStatus) {
	status.QueuePosition = nil
	status.QueueLength = nil
	status.QueuedAt = nil
}

func setBrokerQueueStatus(status *guacamolev1alpha1.DesktopSessionStatus, position, length int32, now metav1.Time) {
	status.Phase = guacamolev1alpha1.DesktopSessionPhaseQueued
	status.QueuePosition = &position
	status.QueueLength = &length
	if status.QueuedAt == nil {
		status.QueuedAt = &now
	}
	if position <= 1 {
		status.Message = "next in line; waiting for an Available desktop"
	} else {
		status.Message = "queued for next Available desktop"
	}
	setDesktopSessionCondition(status, "Ready", metav1.ConditionFalse, "Queued", status.Message)
}

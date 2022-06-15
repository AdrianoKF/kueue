/*
Copyright 2022 The Kubernetes Authors.

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

package cache

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1alpha1"
	"sigs.k8s.io/kueue/pkg/util/pointer"
	utiltesting "sigs.k8s.io/kueue/pkg/util/testing"
	"sigs.k8s.io/kueue/pkg/workload"
)

func TestCacheClusterQueueOperations(t *testing.T) {
	initialClusterQueues := []kueue.ClusterQueue{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "a"},
			Spec: kueue.ClusterQueueSpec{
				Resources: []kueue.Resource{
					{
						Name: corev1.ResourceCPU,
						Flavors: []kueue.Flavor{{
							Name: "default",
							Quota: kueue.Quota{
								Min: resource.MustParse("10"),
								Max: pointer.Quantity(resource.MustParse("20")),
							},
						}},
					},
				},
				Cohort: "one",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "b"},
			Spec: kueue.ClusterQueueSpec{
				Resources: []kueue.Resource{
					{
						Name: corev1.ResourceCPU,
						Flavors: []kueue.Flavor{{
							Name: "default",
							Quota: kueue.Quota{
								Min: resource.MustParse("15"),
							},
						}},
					},
				},
				Cohort: "one",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "c"},
			Spec:       kueue.ClusterQueueSpec{Cohort: "two"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "d"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "e"},
			Spec: kueue.ClusterQueueSpec{
				Resources: []kueue.Resource{
					{
						Name: corev1.ResourceCPU,
						Flavors: []kueue.Flavor{{
							Name: "nonexistent-flavor",
							Quota: kueue.Quota{
								Min: resource.MustParse("15"),
							},
						}},
					},
				},
				Cohort: "two",
			},
		},
	}
	setup := func(cache *Cache) {
		cache.AddOrUpdateResourceFlavor(&kueue.ResourceFlavor{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Labels:     map[string]string{"cpuType": "default"},
		})
		for _, c := range initialClusterQueues {
			if err := cache.AddClusterQueue(context.Background(), &c); err != nil {
				t.Fatalf("Failed adding ClusterQueue: %v", err)
			}
		}
	}
	scheme := runtime.NewScheme()
	if err := kueue.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed adding kueue scheme: %v", err)
	}
	cases := []struct {
		name              string
		operation         func(*Cache)
		wantClusterQueues map[string]*ClusterQueue
		wantCohorts       map[string]sets.String
	}{
		{
			name: "add",
			operation: func(cache *Cache) {
				setup(cache)
			},
			wantClusterQueues: map[string]*ClusterQueue{
				"a": {
					Name: "a",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 10000, Max: pointer.Int64(20000)}},
					},
					NamespaceSelector: labels.Nothing(),
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType")},
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					Status:            Active,
				},
				"b": {
					Name: "b",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 15000}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType")},
					Status:            Active,
				},
				"c": {
					Name:                 "c",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"d": {
					Name:                 "d",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"e": {
					Name: "e",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "nonexistent-flavor", Min: 15000}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"nonexistent-flavor": 0}},
					LabelKeys:         nil,
					Status:            Pending,
				},
			},
			wantCohorts: map[string]sets.String{
				"one": sets.NewString("a", "b"),
				"two": sets.NewString("c", "e"),
			},
		},
		{
			name: "add flavors after queue capacities",
			operation: func(cache *Cache) {
				for _, c := range initialClusterQueues {
					if err := cache.AddClusterQueue(context.Background(), &c); err != nil {
						t.Fatalf("Failed adding ClusterQueue: %v", err)
					}
				}
				cache.AddOrUpdateResourceFlavor(&kueue.ResourceFlavor{
					ObjectMeta: metav1.ObjectMeta{Name: "default"},
					Labels:     map[string]string{"cpuType": "default"},
				})
			},
			wantClusterQueues: map[string]*ClusterQueue{
				"a": {
					Name: "a",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 10000, Max: pointer.Int64(20000)}},
					},
					NamespaceSelector: labels.Nothing(),
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType")},
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					Status:            Active,
				},
				"b": {
					Name: "b",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 15000}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType")},
					Status:            Active,
				},
				"c": {
					Name:                 "c",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"d": {
					Name:                 "d",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"e": {
					Name: "e",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "nonexistent-flavor", Min: 15000}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"nonexistent-flavor": 0}},
					LabelKeys:         nil,
					Status:            Pending,
				},
			},
			wantCohorts: map[string]sets.String{
				"one": sets.NewString("a", "b"),
				"two": sets.NewString("c", "e"),
			},
		},
		{
			name: "update",
			operation: func(cache *Cache) {
				setup(cache)
				clusterQueues := []kueue.ClusterQueue{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "a"},
						Spec: kueue.ClusterQueueSpec{
							Resources: []kueue.Resource{
								{
									Name: corev1.ResourceCPU,
									Flavors: []kueue.Flavor{
										{
											Name: "default",
											Quota: kueue.Quota{
												Min: resource.MustParse("5"),
												Max: pointer.Quantity(resource.MustParse("10")),
											},
										},
									},
								}},
							Cohort: "two",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "b"},
						Spec: kueue.ClusterQueueSpec{
							// remove the only flavor
							Cohort:            "one",                   // No change.
							NamespaceSelector: &metav1.LabelSelector{}, // everything
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "e"},
						Spec: kueue.ClusterQueueSpec{
							Resources: []kueue.Resource{
								{
									Name: corev1.ResourceCPU,
									Flavors: []kueue.Flavor{
										{
											Name: "default",
											Quota: kueue.Quota{
												Min: resource.MustParse("5"),
												Max: pointer.Quantity(resource.MustParse("10")),
											},
										},
									},
								}},
							Cohort: "two",
						},
					},
				}
				for _, c := range clusterQueues {
					if err := cache.UpdateClusterQueue(&c); err != nil {
						t.Fatalf("Failed updating ClusterQueue: %v", err)
					}
				}
				cache.AddOrUpdateResourceFlavor(&kueue.ResourceFlavor{
					ObjectMeta: metav1.ObjectMeta{Name: "default"},
					Labels:     map[string]string{"cpuType": "default", "region": "central"},
				})
			},
			wantClusterQueues: map[string]*ClusterQueue{
				"a": {
					Name: "a",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 5000, Max: pointer.Int64(10000)}},
					},
					NamespaceSelector: labels.Nothing(),
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType", "region")},
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					Status:            Active,
				},
				"b": {
					Name:                 "b",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Everything(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"c": {
					Name:                 "c",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"d": {
					Name:                 "d",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"e": {
					Name: "e",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 5000, Max: pointer.Int64(10000)}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType", "region")},
					Status:            Active,
				},
			},
			wantCohorts: map[string]sets.String{
				"one": sets.NewString("b"),
				"two": sets.NewString("a", "c", "e"),
			},
		},
		{
			name: "delete",
			operation: func(cache *Cache) {
				setup(cache)
				clusterQueues := []kueue.ClusterQueue{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
				}
				for _, c := range clusterQueues {
					cache.DeleteClusterQueue(&c)
				}
			},
			wantClusterQueues: map[string]*ClusterQueue{
				"b": {
					Name: "b",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 15000}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType")},
					Status:            Active,
				},
				"c": {
					Name:                 "c",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"e": {
					Name: "e",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "nonexistent-flavor", Min: 15000}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"nonexistent-flavor": 0}},
					LabelKeys:         nil,
					Status:            Pending,
				},
			},
			wantCohorts: map[string]sets.String{
				"one": sets.NewString("b"),
				"two": sets.NewString("c", "e"),
			},
		},
		{
			name: "add resource flavors",
			operation: func(cache *Cache) {
				setup(cache)
				cache.AddOrUpdateResourceFlavor(&kueue.ResourceFlavor{
					ObjectMeta: metav1.ObjectMeta{Name: "nonexistent-flavor"},
				})
			},
			wantClusterQueues: map[string]*ClusterQueue{
				"a": {
					Name: "a",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 10000, Max: pointer.Int64(20000)}},
					},
					NamespaceSelector: labels.Nothing(),
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType")},
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					Status:            Active,
				},
				"b": {
					Name: "b",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "default", Min: 15000}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"default": 0}},
					LabelKeys:         map[corev1.ResourceName]sets.String{corev1.ResourceCPU: sets.NewString("cpuType")},
					Status:            Active,
				},
				"c": {
					Name:                 "c",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"d": {
					Name:                 "d",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{},
					NamespaceSelector:    labels.Nothing(),
					UsedResources:        Resources{},
					Status:               Active,
				},
				"e": {
					Name: "e",
					RequestableResources: map[corev1.ResourceName][]FlavorLimits{
						corev1.ResourceCPU: {{Name: "nonexistent-flavor", Min: 15000}},
					},
					NamespaceSelector: labels.Nothing(),
					UsedResources:     Resources{corev1.ResourceCPU: {"nonexistent-flavor": 0}},
					LabelKeys:         nil,
					Status:            Active,
				},
			},
			wantCohorts: map[string]sets.String{
				"one": sets.NewString("a", "b"),
				"two": sets.NewString("c", "e"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := New(fake.NewClientBuilder().WithScheme(scheme).Build())
			tc.operation(cache)
			if diff := cmp.Diff(tc.wantClusterQueues, cache.clusterQueues,
				cmpopts.IgnoreFields(ClusterQueue{}, "Cohort", "Workloads")); diff != "" {
				t.Errorf("Unexpected clusterQueues (-want,+got):\n%s", diff)
			}

			gotCohorts := map[string]sets.String{}
			for name, cohort := range cache.cohorts {
				gotCohort := sets.NewString()
				for cq := range cohort.members {
					gotCohort.Insert(cq.Name)
				}
				gotCohorts[name] = gotCohort
			}
			if diff := cmp.Diff(tc.wantCohorts, gotCohorts); diff != "" {
				t.Errorf("Unexpected cohorts (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestCacheWorkloadOperations(t *testing.T) {
	clusterQueues := []kueue.ClusterQueue{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "one"},
			Spec: kueue.ClusterQueueSpec{
				Resources: []kueue.Resource{
					{
						Name: "cpu",
						Flavors: []kueue.Flavor{
							{Name: "on-demand"},
							{Name: "spot"},
						},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "two"},
			Spec: kueue.ClusterQueueSpec{
				Resources: []kueue.Resource{
					{
						Name: "cpu",
						Flavors: []kueue.Flavor{
							{Name: "on-demand"},
							{Name: "spot"},
						},
					},
				},
			},
		},
	}
	podSets := []kueue.PodSet{
		{
			Name: "driver",
			Spec: utiltesting.PodSpecForRequest(map[corev1.ResourceName]string{
				corev1.ResourceCPU:    "10m",
				corev1.ResourceMemory: "512Ki",
			}),
			Count: 1,
		},
		{
			Name: "workers",
			Spec: utiltesting.PodSpecForRequest(
				map[corev1.ResourceName]string{
					corev1.ResourceCPU: "5m",
				}),
			Count: 3,
		},
	}
	podSetFlavors := []kueue.PodSetFlavors{
		{
			Name: "driver",
			Flavors: map[corev1.ResourceName]string{
				corev1.ResourceCPU: "on-demand",
			},
		},
		{
			Name: "workers",
			Flavors: map[corev1.ResourceName]string{
				corev1.ResourceCPU: "spot",
			},
		},
	}
	scheme := runtime.NewScheme()
	if err := kueue.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed adding kueue scheme: %v", err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		utiltesting.MakeWorkload("a", "").PodSets(podSets).Admit(&kueue.Admission{
			ClusterQueue:  "one",
			PodSetFlavors: podSetFlavors,
		}).Obj(),
		utiltesting.MakeWorkload("b", "").Admit(&kueue.Admission{
			ClusterQueue: "one",
		}).Obj(),
		utiltesting.MakeWorkload("c", "").PodSets(podSets).Admit(&kueue.Admission{
			ClusterQueue: "two",
		}).Obj(),
	).Build()

	type result struct {
		Workloads        sets.String
		AssumedWorkloads sets.String
		UsedResources    Resources
	}

	steps := []struct {
		name                 string
		operation            func(cache *Cache) error
		wantResults          map[string]result
		wantAssumedWorkloads map[string]string
		wantError            string
	}{
		{
			name: "add",
			operation: func(cache *Cache) error {
				workloads := []*kueue.Workload{
					utiltesting.MakeWorkload("a", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "one",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
					utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
						ClusterQueue: "two",
					}).Obj(),
				}
				for i := range workloads {
					if !cache.AddOrUpdateWorkload(workloads[i]) {
						return fmt.Errorf("failed to add workload")
					}
				}
				return nil
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c", "d"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "add error clusterQueue doesn't exist",
			operation: func(cache *Cache) error {
				w := utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
					ClusterQueue: "three",
				}).Obj()
				if !cache.AddOrUpdateWorkload(w) {
					return fmt.Errorf("failed to add workload")
				}
				return nil
			},
			wantError: "failed to add workload",
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "add already exists",
			operation: func(cache *Cache) error {
				w := utiltesting.MakeWorkload("b", "").Admit(&kueue.Admission{
					ClusterQueue: "one",
				}).Obj()
				if !cache.AddOrUpdateWorkload(w) {
					return fmt.Errorf("failed to add workload")
				}
				return nil
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "update",
			operation: func(cache *Cache) error {
				old := utiltesting.MakeWorkload("a", "").Admit(&kueue.Admission{
					ClusterQueue: "one",
				}).Obj()
				latest := utiltesting.MakeWorkload("a", "").PodSets(podSets).Admit(&kueue.Admission{
					ClusterQueue:  "two",
					PodSetFlavors: podSetFlavors,
				}).Obj()
				return cache.UpdateWorkload(old, latest)
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("b"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
				"two": {
					Workloads:     sets.NewString("a", "c"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
			},
		},
		{
			name: "update error old clusterQueue doesn't exist",
			operation: func(cache *Cache) error {
				old := utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
					ClusterQueue: "three",
				}).Obj()
				latest := utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
					ClusterQueue: "one",
				}).Obj()
				return cache.UpdateWorkload(old, latest)
			},
			wantError: "old ClusterQueue doesn't exist",
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "update error new clusterQueue doesn't exist",
			operation: func(cache *Cache) error {
				old := utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
					ClusterQueue: "one",
				}).Obj()
				latest := utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
					ClusterQueue: "three",
				}).Obj()
				return cache.UpdateWorkload(old, latest)
			},
			wantError: "new ClusterQueue doesn't exist",
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "update workload which doesn't exist.",
			operation: func(cache *Cache) error {
				old := utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
					ClusterQueue: "one",
				}).Obj()
				latest := utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
					ClusterQueue: "two",
				}).Obj()
				return cache.UpdateWorkload(old, latest)
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c", "d"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "delete",
			operation: func(cache *Cache) error {
				w := utiltesting.MakeWorkload("a", "").Admit(&kueue.Admission{
					ClusterQueue: "one",
				}).Obj()
				return cache.DeleteWorkload(w)
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("b"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "delete error clusterQueue doesn't exist",
			operation: func(cache *Cache) error {
				w := utiltesting.MakeWorkload("a", "").Admit(&kueue.Admission{
					ClusterQueue: "three",
				}).Obj()
				return cache.DeleteWorkload(w)
			},
			wantError: "cluster queue not found",
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "delete workload which doesn't exist",
			operation: func(cache *Cache) error {
				w := utiltesting.MakeWorkload("d", "").Admit(&kueue.Admission{
					ClusterQueue: "one",
				}).Obj()
				return cache.DeleteWorkload(w)
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "assume",
			operation: func(cache *Cache) error {
				workloads := []*kueue.Workload{
					utiltesting.MakeWorkload("d", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "one",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
					utiltesting.MakeWorkload("e", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "two",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
				}
				for i := range workloads {
					if err := cache.AssumeWorkload(workloads[i]); err != nil {
						return err
					}
				}
				return nil
			},
			wantResults: map[string]result{
				"one": {
					Workloads:        sets.NewString("a", "b", "d"),
					AssumedWorkloads: sets.NewString("/d"),
					UsedResources:    Resources{"cpu": {"on-demand": 20, "spot": 30}},
				},
				"two": {
					Workloads:        sets.NewString("c", "e"),
					AssumedWorkloads: sets.NewString("/e"),
					UsedResources:    Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
			},
			wantAssumedWorkloads: map[string]string{
				"/d": "one",
				"/e": "two",
			},
		},
		{
			name: "assume existing workload",
			operation: func(cache *Cache) error {
				workload := utiltesting.MakeWorkload("a", "").PodSets(podSets).Admit(&kueue.Admission{
					ClusterQueue:  "one",
					PodSetFlavors: podSetFlavors,
				}).Obj()
				return cache.AssumeWorkload(workload)
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
			wantError: "workload already exists in ClusterQueue",
		},
		{
			name: "assume error clusterQueue doesn't exist",
			operation: func(cache *Cache) error {
				w := utiltesting.MakeWorkload("d", "").PodSets(podSets).Admit(&kueue.Admission{
					ClusterQueue: "three",
				}).Obj()
				if err := cache.AssumeWorkload(w); err != nil {
					return err
				}
				return nil
			},
			wantError: "cluster queue not found",
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
			wantAssumedWorkloads: map[string]string{},
		},
		{
			name: "update a workload's clusterQueue",
			operation: func(cache *Cache) error {
				workloads := []*kueue.Workload{
					utiltesting.MakeWorkload("d", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "one",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
					utiltesting.MakeWorkload("e", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "two",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
				}
				for i := range workloads {
					if err := cache.AssumeWorkload(workloads[i]); err != nil {
						return err
					}
				}

				newWorkload := utiltesting.MakeWorkload("d", "").PodSets(podSets).Admit(&kueue.Admission{
					ClusterQueue: "two",
				}).Obj()
				return cache.UpdateWorkload(workloads[0], newWorkload)
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:        sets.NewString("c", "d", "e"),
					AssumedWorkloads: sets.NewString("/e"),
					UsedResources:    Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
			},
			wantAssumedWorkloads: map[string]string{
				"/e": "two",
			},
		},
		{
			name: "forget",
			operation: func(cache *Cache) error {
				workloads := []*kueue.Workload{
					utiltesting.MakeWorkload("d", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "one",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
					utiltesting.MakeWorkload("e", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "two",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
				}
				for i := range workloads {
					if err := cache.AssumeWorkload(workloads[i]); err != nil {
						return err
					}
				}

				w := workloads[0]
				return cache.ForgetWorkload(w)
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:        sets.NewString("c", "e"),
					AssumedWorkloads: sets.NewString("/e"),
					UsedResources:    Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
			},
			wantAssumedWorkloads: map[string]string{
				"/e": "two",
			},
		},
		{
			name: "forget error workload is not assumed",
			operation: func(cache *Cache) error {
				w := utiltesting.MakeWorkload("b", "").Admit(&kueue.Admission{
					ClusterQueue: "one",
				}).Obj()
				if err := cache.ForgetWorkload(w); err != nil {
					return err
				}
				return nil
			},
			wantError: "the workload is not assumed",
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:     sets.NewString("c"),
					UsedResources: Resources{"cpu": {"on-demand": 0, "spot": 0}},
				},
			},
		},
		{
			name: "add assumed workload",
			operation: func(cache *Cache) error {
				workloads := []*kueue.Workload{
					utiltesting.MakeWorkload("d", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "one",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
					utiltesting.MakeWorkload("e", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "two",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
				}
				for i := range workloads {
					if err := cache.AssumeWorkload(workloads[i]); err != nil {
						return err
					}
				}

				w := workloads[0]
				if !cache.AddOrUpdateWorkload(w) {
					return fmt.Errorf("failed to add workload")
				}
				return nil
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b", "d"),
					UsedResources: Resources{"cpu": {"on-demand": 20, "spot": 30}},
				},
				"two": {
					Workloads:        sets.NewString("c", "e"),
					AssumedWorkloads: sets.NewString("/e"),
					UsedResources:    Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
			},
			wantAssumedWorkloads: map[string]string{
				"/e": "two",
			},
		},
		{
			name: "delete assumed workload",
			operation: func(cache *Cache) error {
				workloads := []*kueue.Workload{
					utiltesting.MakeWorkload("d", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "one",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
					utiltesting.MakeWorkload("e", "").PodSets(podSets).Admit(&kueue.Admission{
						ClusterQueue:  "two",
						PodSetFlavors: podSetFlavors,
					}).Obj(),
				}
				for i := range workloads {
					if err := cache.AssumeWorkload(workloads[i]); err != nil {
						return err
					}
				}
				return cache.DeleteWorkload(workloads[0])
			},
			wantResults: map[string]result{
				"one": {
					Workloads:     sets.NewString("a", "b"),
					UsedResources: Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
				"two": {
					Workloads:        sets.NewString("c", "e"),
					AssumedWorkloads: sets.NewString("/e"),
					UsedResources:    Resources{"cpu": {"on-demand": 10, "spot": 15}},
				},
			},
			wantAssumedWorkloads: map[string]string{
				"/e": "two",
			},
		},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			cache := New(client)

			for _, c := range clusterQueues {
				if err := cache.AddClusterQueue(context.Background(), &c); err != nil {
					t.Fatalf("Failed adding clusterQueue: %v", err)
				}
			}

			gotError := step.operation(cache)
			if diff := cmp.Diff(step.wantError, messageOrEmpty(gotError)); diff != "" {
				t.Errorf("Unexpected error (-want,+got):\n%s", diff)
			}
			gotWorkloads := make(map[string]result)
			for name, cq := range cache.clusterQueues {
				c := sets.NewString()
				for k := range cq.Workloads {
					c.Insert(cq.Workloads[k].Obj.Name)
				}
				gotWorkloads[name] = result{
					Workloads:        c,
					AssumedWorkloads: cq.AssumedWorkloads,
					UsedResources:    cq.UsedResources,
				}
			}
			if diff := cmp.Diff(step.wantResults, gotWorkloads); diff != "" {
				t.Errorf("Unexpected clusterQueues (-want,+got):\n%s", diff)
			}
			if step.wantAssumedWorkloads == nil {
				step.wantAssumedWorkloads = map[string]string{}
			}
			if diff := cmp.Diff(step.wantAssumedWorkloads, cache.assumedWorkloads); diff != "" {
				t.Errorf("Unexpected assumed workloads (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestClusterQueueUsage(t *testing.T) {
	cq := kueue.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "foo"},
		Spec: kueue.ClusterQueueSpec{
			Resources: []kueue.Resource{
				{
					Name: corev1.ResourceCPU,
					Flavors: []kueue.Flavor{
						{
							Name: "default",
							Quota: kueue.Quota{
								Min: resource.MustParse("10"),
								Max: pointer.Quantity(resource.MustParse("20")),
							},
						},
					},
				},
				{
					Name: "example.com/gpu",
					Flavors: []kueue.Flavor{
						{
							Name: "model_a",
							Quota: kueue.Quota{
								Min: resource.MustParse("5"),
								Max: pointer.Quantity(resource.MustParse("10")),
							},
						},
						{
							Name: "model_b",
							Quota: kueue.Quota{
								Min: resource.MustParse("5"),
								// No max.
							},
						},
					},
				},
			},
		},
	}
	workloads := []kueue.Workload{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "one"},
			Spec: kueue.WorkloadSpec{
				PodSets: []kueue.PodSet{
					{
						Name:  "main",
						Count: 1,
						Spec: utiltesting.PodSpecForRequest(map[corev1.ResourceName]string{
							corev1.ResourceCPU: "8",
							"example.com/gpu":  "5",
						}),
					},
				},
				Admission: &kueue.Admission{
					ClusterQueue: "foo",
					PodSetFlavors: []kueue.PodSetFlavors{
						{
							Name: "main",
							Flavors: map[corev1.ResourceName]string{
								corev1.ResourceCPU: "default",
								"example.com/gpu":  "model_a",
							},
						},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "two"},
			Spec: kueue.WorkloadSpec{
				PodSets: []kueue.PodSet{
					{
						Name:  "main",
						Count: 1,
						Spec: utiltesting.PodSpecForRequest(map[corev1.ResourceName]string{
							corev1.ResourceCPU: "5",
							"example.com/gpu":  "6",
						}),
					},
				},
				Admission: &kueue.Admission{
					ClusterQueue: "foo",
					PodSetFlavors: []kueue.PodSetFlavors{
						{
							Name: "main",
							Flavors: map[corev1.ResourceName]string{
								corev1.ResourceCPU: "default",
								"example.com/gpu":  "model_b",
							},
						},
					},
				},
			},
		},
	}
	cases := map[string]struct {
		workloads         []kueue.Workload
		wantUsedResources kueue.UsedResources
		wantWorkloads     int
	}{
		"single no borrowing": {
			workloads: workloads[:1],
			wantUsedResources: kueue.UsedResources{
				corev1.ResourceCPU: {
					"default": kueue.Usage{
						Total: pointer.Quantity(resource.MustParse("8")),
					},
				},
				"example.com/gpu": {
					"model_a": kueue.Usage{
						Total: pointer.Quantity(resource.MustParse("5")),
					},
					"model_b": kueue.Usage{
						Total: pointer.Quantity(resource.MustParse("0")),
					},
				},
			},
			wantWorkloads: 1,
		},
		"multiple borrowing": {
			workloads: workloads,
			wantUsedResources: kueue.UsedResources{
				corev1.ResourceCPU: {
					"default": kueue.Usage{
						Total:    pointer.Quantity(resource.MustParse("13")),
						Borrowed: pointer.Quantity(resource.MustParse("3")),
					},
				},
				"example.com/gpu": {
					"model_a": kueue.Usage{
						Total: pointer.Quantity(resource.MustParse("5")),
					},
					"model_b": kueue.Usage{
						Total:    pointer.Quantity(resource.MustParse("6")),
						Borrowed: pointer.Quantity(resource.MustParse("1")),
					},
				},
			},
			wantWorkloads: 2,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := kueue.AddToScheme(scheme); err != nil {
				t.Fatalf("Failed adding kueue scheme: %v", err)
			}
			cache := New(fake.NewClientBuilder().WithScheme(scheme).Build())
			ctx := context.Background()
			err := cache.AddClusterQueue(ctx, &cq)
			if err != nil {
				t.Fatalf("Adding ClusterQueue: %v", err)
			}
			for _, w := range tc.workloads {
				if added := cache.AddOrUpdateWorkload(&w); !added {
					t.Fatalf("Workload %s was not added", workload.Key(&w))
				}
			}
			resources, workloads, err := cache.Usage(&cq)
			if err != nil {
				t.Fatalf("Couldn't get usage: %v", err)
			}
			if diff := cmp.Diff(tc.wantUsedResources, resources); diff != "" {
				t.Errorf("Unexpected used resources (-want,+got):\n%s", diff)
			}
			if workloads != tc.wantWorkloads {
				t.Errorf("Got %d workloads, want %d", workloads, tc.wantWorkloads)
			}
		})
	}
}

func messageOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

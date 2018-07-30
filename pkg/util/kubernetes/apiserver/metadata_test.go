// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2018 Datadog, Inc.

// +build kubeapiserver

package apiserver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	utilcache "github.com/DataDog/datadog-agent/pkg/util/cache"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func alwaysReady() bool { return true }

func TestMetadataControllerSyncEndpoints(t *testing.T) {
	client := fake.NewSimpleClientset()

	metaController, informerFactory := newFakeMetadataController(client)

	stop := make(chan struct{})
	defer close(stop)
	informerFactory.Start(stop)
	go metaController.Run(stop)

	pod1 := newFakePod(
		"default",
		"pod1_name",
		"1111",
		"1.1.1.1",
	)

	pod2 := newFakePod(
		"default",
		"pod2_name",
		"2222",
		"2.2.2.2",
	)

	pod3 := newFakePod(
		"datadog-system",
		"pod3_name",
		"3333",
		"3.3.3.3",
	)

	// Bootstrap informer with all test nodes.
	for _, node := range []*v1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "node1"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node2"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node3"}},
	} {
		err := informerFactory.
			Core().
			V1().
			Nodes().
			Informer().
			GetIndexer().
			Add(node)
		require.NoError(t, err)
	}

	// The side effects of each test case is cumulative on the cache.
	tests := []struct {
		desc            string
		delete          bool // whether to add or delete endpoints
		endpoints       []*v1.Endpoints
		expectedBundles map[string]ServicesMapper
	}{
		// Add
		{
			"one service on multiple nodes",
			false,
			[]*v1.Endpoints{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "svc1"},
					Subsets: []v1.EndpointSubset{
						{
							Addresses: []v1.EndpointAddress{
								newFakeEndpointAddress("node1", pod1),
								newFakeEndpointAddress("node2", pod2),
							},
						},
					},
				},
			},
			map[string]ServicesMapper{
				"node1": ServicesMapper{
					"default": {
						"pod1_name": sets.NewString("svc1"),
					},
				},
				"node2": ServicesMapper{
					"default": {
						"pod2_name": sets.NewString("svc1"),
					},
				},
			},
		},
		{
			"pod added to existing service and node",
			false,
			[]*v1.Endpoints{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "svc1"},
					Subsets: []v1.EndpointSubset{
						{
							Addresses: []v1.EndpointAddress{
								newFakeEndpointAddress("node1", pod3),
							},
						},
					},
				},
			},
			map[string]ServicesMapper{
				"node1": ServicesMapper{
					"default": {
						"pod1_name": sets.NewString("svc1"),
					},
					"datadog-system": {
						"pod3_name": sets.NewString("svc1"),
					},
				},
				"node2": ServicesMapper{
					"default": {
						"pod2_name": sets.NewString("svc1"),
					},
				},
			},
		},
		{
			"add service to existing node and pod",
			false,
			[]*v1.Endpoints{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "svc2"},
					Subsets: []v1.EndpointSubset{
						{
							Addresses: []v1.EndpointAddress{
								newFakeEndpointAddress("node1", pod1),
							},
						},
					},
				},
			},
			map[string]ServicesMapper{
				"node1": ServicesMapper{
					"default": {
						"pod1_name": sets.NewString("svc1", "svc2"),
					},
					"datadog-system": {
						"pod3_name": sets.NewString("svc1"),
					},
				},
				"node2": ServicesMapper{
					"default": {
						"pod2_name": sets.NewString("svc1"),
					},
				},
			},
		},
		// Delete
		{
			"delete service with pods on multiple nodes",
			true,
			[]*v1.Endpoints{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "svc1"},
				},
			},
			map[string]ServicesMapper{
				"node1": ServicesMapper{
					"default": {
						"pod1_name": sets.NewString("svc2"),
					},
					"datadog-system": {
						"pod3_name": sets.NewString("svc1"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		for _, endpoints := range tt.endpoints {
			indexer := informerFactory.
				Core().
				V1().
				Endpoints().
				Informer().
				GetIndexer()

			var err error
			if tt.delete {
				err = indexer.Delete(endpoints)
			} else {
				err = indexer.Add(endpoints)
			}
			require.NoError(t, err)

			key, err := cache.MetaNamespaceKeyFunc(endpoints)
			require.NoError(t, err)

			err = metaController.syncEndpoints(key)
			require.NoError(t, err)
		}

		for nodeName, expectedMapper := range tt.expectedBundles {
			cacheKey := utilcache.BuildAgentKey(metadataMapperCachePrefix, nodeName)
			v, ok := utilcache.Cache.Get(cacheKey)
			require.True(t, ok, "No meta bundle for %s", nodeName)
			metaBundle, ok := v.(*MetadataMapperBundle)
			require.True(t, ok)

			assert.Equal(t, expectedMapper, metaBundle.Services)
		}
	}
}

func TestMetadataController(t *testing.T) {
	client := fake.NewSimpleClientset()

	metaController, informerFactory := newFakeMetadataController(client)

	metaController.endpoints = make(chan interface{}, 1)

	stop := make(chan struct{})
	defer close(stop)
	informerFactory.Start(stop)
	go metaController.Run(stop)

	c := client.CoreV1()
	require.NotNil(t, c)

	// Create a Ready Schedulable node
	// As we don't have a controller they don't need to have some heartbeat mechanism
	node := &v1.Node{
		Spec: v1.NodeSpec{
			PodCIDR:       "192.168.1.0/24",
			Unschedulable: false,
		},
		Status: v1.NodeStatus{
			Addresses: []v1.NodeAddress{
				{
					Address: "172.31.119.125",
					Type:    "InternalIP",
				},
				{
					Address: "ip-172-31-119-125.eu-west-1.compute.internal",
					Type:    "InternalDNS",
				},
				{
					Address: "ip-172-31-119-125.eu-west-1.compute.internal",
					Type:    "Hostname",
				},
			},
			Conditions: []v1.NodeCondition{
				{
					Type:    "Ready",
					Status:  "True",
					Reason:  "KubeletReady",
					Message: "kubelet is posting ready status",
				},
			},
		},
	}
	node.Name = "ip-172-31-119-125"
	_, err := c.Nodes().Create(node)
	require.NoError(t, err)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
		},
		Spec: v1.PodSpec{
			NodeName: node.Name,
			Containers: []v1.Container{
				{
					Name:  "nginx",
					Image: "nginx:latest",
				},
			},
		},
	}
	pod.Name = "nginx"
	pod.Labels = map[string]string{"app": "nginx"}
	pendingPod, err := c.Pods("default").Create(pod)
	require.NoError(t, err)

	pendingPod.Status = v1.PodStatus{
		Phase:  "Running",
		PodIP:  "172.17.0.1",
		HostIP: "172.31.119.125",
		Conditions: []v1.PodCondition{
			{
				Type:   "Ready",
				Status: "True",
			},
		},
		// mark it ready
		ContainerStatuses: []v1.ContainerStatus{
			{
				Name:  "nginx",
				Ready: true,
				Image: "nginx:latest",
				State: v1.ContainerState{Running: &v1.ContainerStateRunning{StartedAt: metav1.Now()}},
			},
		},
	}
	_, err = c.Pods("default").UpdateStatus(pendingPod)
	require.NoError(t, err)

	svc := &v1.Service{
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"app": "nginx",
			},
			Ports: []v1.ServicePort{{Port: 443}},
		},
	}
	svc.Name = "nginx-1"
	_, err = c.Services("default").Create(svc)
	require.NoError(t, err)

	ep := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       pendingPod.Status.PodIP,
						NodeName: &node.Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: pendingPod.Namespace,
							Name:      pendingPod.Name,
							UID:       pendingPod.UID,
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "https",
						Port:     443,
						Protocol: "TCP",
					},
				},
			},
		},
	}
	ep.Name = "nginx-1"
	_, err = c.Endpoints("default").Create(ep)
	require.NoError(t, err)

	timeout := time.NewTimer(2 * time.Second)

	// wait for endpoints to be synced by controller
	select {
	case <-metaController.endpoints:
	case <-timeout.C:
		require.FailNow(t, "Timeout waiting for endpoints to sync")
	}

	metadataNames, err := GetPodMetadataNames(node.Name, pod.Namespace, pod.Name)
	require.NoError(t, err)
	assert.Len(t, metadataNames, 1)
	assert.Contains(t, metadataNames, "kube_service:nginx-1")

	// Add a new service/endpoint on the nginx Pod
	svc.Name = "nginx-2"
	_, err = c.Services("default").Create(svc)
	require.NoError(t, err)

	ep.Name = "nginx-2"
	_, err = c.Endpoints("default").Create(ep)
	require.NoError(t, err)

	timeout = time.NewTimer(2 * time.Second)

	// wait for endpoints to be synced by controller
	select {
	case <-metaController.endpoints:
	case <-timeout.C:
		require.FailNow(t, "Timeout waiting for endpoints to sync")
	}

	metadataNames, err = GetPodMetadataNames(node.Name, pod.Namespace, pod.Name)
	require.NoError(t, err)
	assert.Len(t, metadataNames, 2)
	assert.Contains(t, metadataNames, "kube_service:nginx-1")
	assert.Contains(t, metadataNames, "kube_service:nginx-2")

	cl := &APIClient{Cl: client, timeoutSeconds: 5}

	fullmapper, errList := GetMetadataMapBundleOnAllNodes(cl)
	require.Nil(t, errList)
	list := fullmapper["Nodes"]
	assert.Contains(t, list, "ip-172-31-119-125")
	fullMap := list.(map[string]*MetadataMapperBundle)
	services, found := fullMap["ip-172-31-119-125"].ServicesForPod(metav1.NamespaceDefault, "nginx")
	assert.True(t, found)
	assert.Contains(t, services, "nginx-1")
}

func newFakeMetadataController(client kubernetes.Interface) (*MetadataController, informers.SharedInformerFactory) {
	informerFactory := informers.NewSharedInformerFactory(client, 0)

	metaController := NewMetadataController(
		informerFactory.Core().V1().Nodes(),
		informerFactory.Core().V1().Endpoints(),
	)
	metaController.nodeListerSynced = alwaysReady
	metaController.endpointsListerSynced = alwaysReady

	return metaController, informerFactory
}

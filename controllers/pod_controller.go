/*
Copyright 2022 pengpeijie.

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

package controllers

import (
	_ "net/http/pprof"
	"sigs.k8s.io/controller-runtime/pkg/builder"

	"context"
	"fmt"
	"github.com/containerd/cgroups"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/namespaces"
	"github.com/go-logr/logr"
	"github.com/opencontainers/runtime-spec/specs-go"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

const (
	ALLOW_BLOCK_KEY = "cgroup-enhancer.device.allow.block"
	CRI_NAMESPACE   = "k8s.io"
)

//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("device allow block", req.NamespacedName)

	done := ctrl.Result{}
	pod := &v1.Pod{}

	r.Log.Info(fmt.Sprintf("pod %v reconcile", req.NamespacedName))
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, pod)

	// pod not found
	if errors.IsNotFound(err) {
		r.Log.Info(fmt.Sprintf("pod %v not found", req.NamespacedName))
		return done, nil
	}
	if err != nil {
		r.Log.Info(fmt.Sprintf("pod %v/%v error : %v", req.Namespace, req.Name, err))
		return done, nil
	}

	// filter NotReady pods
	if pod.DeletionTimestamp != nil || pod.Status.Phase != v1.PodRunning {
		r.Log.Info(fmt.Sprintf("pod %v/%v deleted or not running", req.Namespace, req.Name))
		return done, nil
	}

	value := pod.ObjectMeta.Annotations[ALLOW_BLOCK_KEY]
	if len(value) <= 0 {
		r.Log.Info(fmt.Sprintf("pod %v/%v device.allow block value is empty", req.Namespace, req.Name))
		return done, nil
	}
	r.Log.Info(fmt.Sprintf("pod %v/%v device.allow block value is [%v], try to update cgroup device.allow", req.Namespace, req.Name, value))

	// 异步更新
	go func() {
		// range update
		client, err := containerd.New("/run/containerd/containerd.sock")
		defer client.Close()
		if err != nil {
			r.Log.Error(err, "create containerd client error")
			panic("init containerd(/run/containerd/containerd.sock) client error")
		}
		serving, err := client.IsServing(ctx)
		if err != nil {
			r.Log.Error(err, "containerd client serving error : %v", err)
			return
		}
		if !serving {
			r.Log.Error(err, "containerd client serving false")
			return
		}
		cns := namespaces.WithNamespace(context.Background(), CRI_NAMESPACE)

		for _, containerStatus := range pod.Status.ContainerStatuses {
			// containerd://d68211d72a96a22e62bb7379cb6006f36e98c7ab66e807a958935d71ba31b3e7
			containerIDSlice := strings.SplitN(containerStatus.ContainerID, "://", 2)
			if len(containerIDSlice) != 2 {
				r.Log.Info(fmt.Sprintf("pod(%v/%v) container(%v) containerId invalid : %v", req.Namespace, req.Name, containerStatus.Name, containerStatus.ContainerID))
				continue
			}
			containerId := containerIDSlice[1]
			task, err := client.TaskService().Get(cns, &tasks.GetRequest{
				ContainerID: containerId,
			})
			if err != nil {
				r.Log.Error(err, fmt.Sprintf("pod(%v/%v) container(%v) get task error : %v", req.Namespace, req.Name, containerStatus.ContainerID, err))
				continue
			}
			pid := int(task.Process.Pid)
			r.Log.Info(fmt.Sprintf("pod(%v/%v) container(%v/%v) get task pid : %v", req.Namespace, req.Name, containerStatus.Name, containerStatus.ContainerID, pid))

			path := cgroups.PidPath(pid)

			control, err := cgroups.Load(cgroups.V1, path)
			if err != nil {
				r.Log.Error(err, fmt.Sprintf("pod(%v/%v) container(%v/%v) load cgroup error : %v", req.Namespace, req.Name, containerStatus.Name, containerStatus.ContainerID, err))
				continue
			}
			if control.State() == cgroups.Deleted {
				r.Log.Info(fmt.Sprintf("pod(%v/%v) container(%v/%v) cgroup deleted", req.Namespace, req.Name, containerStatus.Name, containerStatus.ContainerID))
				continue
			}

			err1 := control.Update(&specs.LinuxResources{
				Devices: []specs.LinuxDeviceCgroup{
					{
						Allow:  true,
						Type:   "b",
						Major:  nil,
						Minor:  nil,
						Access: value,
					},
				},
			})
			if err1 != nil {
				r.Log.Error(err1, fmt.Sprintf("pod(%v/%v) container(%v/%v/%v) devices update error : %v", req.Namespace, req.Name, containerStatus.Name, containerStatus.ContainerID, pid, err))
				continue
			}

			r.Log.Info(fmt.Sprintf("pod(%v/%v) container(%v/%v/%v) devices update successful",
				req.Namespace,
				req.Name,
				containerStatus.Name,
				containerStatus.ContainerID,
				pid))
		}
	}()

	return done, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}, &builder.OnlyMetadata).
		Complete(r)
}

/*
Copyright 2024.

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
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"

	inferencev1alpha1 "github.com/openshift/instaslice-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// InstasliceReconciler reconciles a Instaslice object
type InstasliceReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	kubeClient *kubernetes.Clientset
}

// AllocationPolicy interface with a single method
type AllocationPolicy interface {
	SetAllocationDetails(profileName string, newStart, size uint32, podUUID string, nodename string, processed string,
		discoveredGiprofile int, Ciprofileid int, Ciengprofileid int, namespace string, podName string, gpuUuid string, resourceIndetifier string,
		cpumilli int64, memory int64) *inferencev1alpha1.AllocationDetails
}

// not implemented
type RightToLeftPolicy struct{}

// not implemented
type LeftToRightPolicy struct{}

// first fit policy is implemented at the moment
type FirstFitPolicy struct{}

//+kubebuilder:rbac:groups=inference.codeflare.dev,resources=instaslices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=inference.codeflare.dev,resources=instaslices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=inference.codeflare.dev,resources=instaslices/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;create;update;patch;delete

func (r *InstasliceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	var policy AllocationPolicy
	policy = &FirstFitPolicy{}
	pod := &v1.Pod{}
	var isPodGated = false
	err := r.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		// Error fetching the Pod
		if errors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, "unable to fetch pod might be deleted")
			return ctrl.Result{}, nil
		}
		log.FromContext(ctx).Error(err, "unable to fetch pod")
		return ctrl.Result{}, nil
	}

	isPodGated = checkIfPodGated(pod, isPodGated)

	if !isPodGated && !controllerutil.ContainsFinalizer(pod, "org.instaslice/accelarator") {
		//log.FromContext(ctx).Info("Ignoring ", "pod", pod.Name)
		return ctrl.Result{}, nil
	}

	var instasliceList inferencev1alpha1.InstasliceList

	if err := r.List(ctx, &instasliceList, &client.ListOptions{}); err != nil {
		log.FromContext(ctx).Error(err, "Error listing Instaslice")
	}

	// pod is completed move allocation to deleting state and return
	if pod.Status.Phase == v1.PodSucceeded && controllerutil.ContainsFinalizer(pod, "org.instaslice/accelarator") {
		for _, instaslice := range instasliceList.Items {
			for podUuid, allocation := range instaslice.Spec.Allocations {
				if podUuid == string(pod.UID) {
					log.FromContext(ctx).Info("deleting allocation for completed ", "pod", allocation.PodName)
					allocation.Allocationstatus = "deleting"
					var updateInstasliceObject inferencev1alpha1.Instaslice
					typeNamespacedName := types.NamespacedName{
						Name:      instaslice.Name,
						Namespace: "default", // TODO: modify
					}
					err := r.Get(ctx, typeNamespacedName, &updateInstasliceObject)
					if err != nil {
						log.FromContext(ctx).Error(err, "error getting latest instaslice object")
					}
					updateInstasliceObject.Spec.Allocations[podUuid] = allocation
					errUpdatingInstaslice := r.Update(ctx, &updateInstasliceObject)
					if errUpdatingInstaslice != nil {
						log.FromContext(ctx).Info("unable to set instaslice to state deleted for ", "pod", allocation.PodName)
						return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
					}

					latestPod := &v1.Pod{}
					errGettingPod := r.Get(ctx, req.NamespacedName, latestPod)
					if errGettingPod != nil {
						//TODO: should we retry?
						log.FromContext(ctx).Error(errGettingPod, "error getting latest copy of pod")
					}
					errRemovingFinalizer := controllerutil.RemoveFinalizer(latestPod, "org.instaslice/accelarator")
					if !errRemovingFinalizer {
						log.FromContext(ctx).Info("finalizer not deleted for ", "pod", pod.Name)
					}
					if err := r.Update(ctx, latestPod); err != nil {
						log.FromContext(ctx).Info("unable to update removal of finalizer, retrying")
						return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
					}
				}
			}
		}
		return ctrl.Result{}, nil
	}

	// handle deleted pod that never gets ungated
	//set allocation status to deleting to cleanup resources if any
	if !pod.DeletionTimestamp.IsZero() && isPodGated {
		// allocation can be in creating or created while the user deletes the pod.
		for _, instaslice := range instasliceList.Items {
			for podUuid, allocation := range instaslice.Spec.Allocations {
				if podUuid == string(pod.UID) && (allocation.Allocationstatus == "creating" || allocation.Allocationstatus == "created") {
					allocation.Allocationstatus = "deleting"
					var updateInstasliceObject inferencev1alpha1.Instaslice
					typeNamespacedName := types.NamespacedName{
						Name:      instaslice.Name,
						Namespace: "default", // TODO: modify
					}
					err := r.Get(ctx, typeNamespacedName, &updateInstasliceObject)
					if err != nil {
						log.FromContext(ctx).Error(err, "error getting latest instaslice object")
					}
					updateInstasliceObject.Spec.Allocations[podUuid] = allocation
					errUpdatingInstaslice := r.Update(ctx, &updateInstasliceObject)
					if errUpdatingInstaslice != nil {
						log.FromContext(ctx).Info("unable to set instaslice to state deleted for ungated", "pod", allocation.PodName)
						return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
					}
				}
			}
		}
		// allocation is updated to deleting that will trigger daemonset to cleanup
		// remove the finalizer
		if controllerutil.RemoveFinalizer(pod, "org.instaslice/accelarator") {
			if err := r.Update(ctx, pod); err != nil {
				log.FromContext(ctx).Error(err, "unable to update removal of finalizer, retrying")
				// requeing immediately as the finalizer removal gets lost
				return ctrl.Result{Requeue: true}, nil
			}
			log.FromContext(ctx).Info("finalizer deleted")
		}
		return ctrl.Result{}, nil
	}
	// handle graceful termination of pods, wait for about 30 seconds from the time deletiontimestamp is set on the pod
	if !pod.DeletionTimestamp.IsZero() {
		log.FromContext(ctx).Info("set status to deleting for ", "pod", pod.Name)
		if controllerutil.ContainsFinalizer(pod, "org.instaslice/accelarator") {
			for _, instaslice := range instasliceList.Items {
				for podUuid, allocation := range instaslice.Spec.Allocations {
					if podUuid == string(pod.UID) {
						elapsed := time.Since(pod.DeletionTimestamp.Time)
						if elapsed > 30*time.Second {
							allocation.Allocationstatus = "deleting"
							var updateInstasliceObject inferencev1alpha1.Instaslice
							typeNamespacedName := types.NamespacedName{
								Name:      instaslice.Name,
								Namespace: "default", // TODO: modify
							}
							err := r.Get(ctx, typeNamespacedName, &updateInstasliceObject)
							if err != nil {
								log.FromContext(ctx).Error(err, "error getting latest instaslice object")
							}
							updateInstasliceObject.Spec.Allocations[podUuid] = allocation
							errUpdatingInstaslice := r.Update(ctx, &updateInstasliceObject)
							if errUpdatingInstaslice != nil {
								log.FromContext(ctx).Info("unable to set instaslice to state deleted for ", "pod", allocation.PodName)
								return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
							}
							// remove finalizer after switching to deleting status where daemonset can trigger cleanup
							if controllerutil.RemoveFinalizer(pod, "org.instaslice/accelarator") {
								if err := r.Update(ctx, pod); err != nil {
									log.FromContext(ctx).Info("unable to update removal of finalizer, retrying")
									return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
								}
								log.FromContext(ctx).Info("finalizer deleted")
							}
						} else {
							remainingTime := 30*time.Second - elapsed
							return ctrl.Result{RequeueAfter: remainingTime}, nil
						}
					}
				}
			}

		}
		// exit after handling deletion event for a pod.
		return ctrl.Result{}, nil
	}

	// find allocation in the cluster for the pod
	// set allocationstatus to creating when controller adds the allocation
	// check for allocationstatus as created when daemonset is done realizing the slice on the GPU node.
	// set allocationstatus to ungated and ungate the pod so that the workload can begin execution.
	if isPodGated {
		//Assume pod only has one container with one GPU requests
		if len(pod.Spec.Containers) != 1 {
			return ctrl.Result{}, fmt.Errorf("multiple containers per pod not supported")
		}
		limits := pod.Spec.Containers[0].Resources.Limits
		profileName := r.extractProfileName(limits)
		podHasNodeAllocation := false
		// search if pod has allocation in any of the instaslice object in the cluster
		//TODO: allocations may get slower as the cluster size increases
		for _, instaslice := range instasliceList.Items {
			for _, allocations := range instaslice.Spec.Allocations {
				// no matter the state if allocations exists for a pod skip such a pod
				if allocations.PodUUID == string(pod.UID) {
					podHasNodeAllocation = true
				}
			}
		}
		for _, instaslice := range instasliceList.Items {
			for podUuid, allocations := range instaslice.Spec.Allocations {
				if allocations.Allocationstatus == "created" && allocations.PodUUID == string(pod.UID) {
					pod := r.unGatePod(pod)
					errForUngating := r.Update(ctx, pod)
					if errForUngating != nil {
						log.FromContext(ctx).Error(errForUngating, "failed to ungate pod")
						return ctrl.Result{Requeue: true}, nil
					}
					allocations.Allocationstatus = "ungated"
					instaslice.Spec.Allocations[podUuid] = allocations
					var updateInstasliceObject inferencev1alpha1.Instaslice
					typeNamespacedName := types.NamespacedName{
						Name:      instaslice.Name,
						Namespace: "default", // TODO: modify
					}
					errRetrievingInstaSlice := r.Get(ctx, typeNamespacedName, &updateInstasliceObject)
					if errRetrievingInstaSlice != nil {
						log.FromContext(ctx).Error(err, "error getting latest instaslice object")
					}
					if updateInstasliceObject.Spec.Allocations == nil {
						updateInstasliceObject.Spec.Allocations = make(map[string]inferencev1alpha1.AllocationDetails)
					}
					updateInstasliceObject.Spec.Allocations[podUuid] = allocations
					if err := r.Update(ctx, &updateInstasliceObject); err != nil {
						log.FromContext(ctx).Error(err, "Error updating instaslice allocations")
						return ctrl.Result{Requeue: true}, nil
					}
				}
			}
		}
		// pod does not have an allocation yet, make allocation
		// find the node
		if !podHasNodeAllocation {
			for _, instaslice := range instasliceList.Items {
				// find the GPU on the node and the GPU index where the slice can be created
				allocDetails, err := r.findNodeAndDeviceForASlice(ctx, &instaslice, profileName, policy, pod)
				if err != nil {
					continue
				}
				podHasNodeAllocation = true
				for _, item := range instaslice.Spec.Prepared {
					if item.Parent == allocDetails.GPUUUID && item.Size == allocDetails.Size && item.Start == allocDetails.Start {
						log.FromContext(ctx).Info("prepared allocation is yet to be deleted, retrying new allocation")
						return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
					}
				}
				if podHasNodeAllocation {
					var updateInstasliceObject inferencev1alpha1.Instaslice
					typeNamespacedName := types.NamespacedName{
						Name:      instaslice.Name,
						Namespace: "default", // TODO: modify
					}
					err := r.Get(ctx, typeNamespacedName, &updateInstasliceObject)
					if err != nil {
						log.FromContext(ctx).Error(err, "error getting latest instaslice object")
					}
					log.FromContext(ctx).Info("allocation obtained for ", "pod", allocDetails.PodName)
					if updateInstasliceObject.Spec.Allocations == nil {
						updateInstasliceObject.Spec.Allocations = make(map[string]inferencev1alpha1.AllocationDetails)
					}
					updateInstasliceObject.Spec.Allocations[string(pod.UID)] = *allocDetails
					if err := r.Update(ctx, &updateInstasliceObject); err != nil {
						log.FromContext(ctx).Error(err, "Error updating instaslice allocations")
						return ctrl.Result{Requeue: true}, nil
					}
					//allocation was successful
					return ctrl.Result{}, nil
				}
			}
		}

		//if the cluster does not have suitable node, requeue request
		if !podHasNodeAllocation {
			log.FromContext(ctx).Info("no suitable node found in cluster for ", "pod", pod.Name)
			// Generate a random duration between 1 and 10 seconds
			randomDuration := time.Duration(rand.Intn(10)+1) * time.Second
			return ctrl.Result{RequeueAfter: randomDuration}, nil
		}

	}

	return ctrl.Result{}, nil
}

// Extract profile name from the container limits spec
func (*InstasliceReconciler) extractProfileName(limits v1.ResourceList) string {
	profileName := ""
	for k, _ := range limits {
		if strings.Contains(k.String(), "nvidia") {

			re := regexp.MustCompile(`(\d+g\.\d+gb)`)
			match := re.FindStringSubmatch(k.String())
			if len(match) > 1 {
				profileName = match[1]
			} else {
				log.Log.Info("No match found")
			}
		}
	}
	return profileName
}

// Extract NVML specific attributes for GPUs, this will change for different generations of the GPU.
func (*InstasliceReconciler) extractGpuProfile(instaslice *inferencev1alpha1.Instaslice, profileName string) (int, int, int, int) {
	var size int
	var discoveredGiprofile int
	var Ciprofileid int
	var Ciengprofileid int
	for _, item := range instaslice.Spec.Migplacement {
		if item.Profile == profileName {
			for _, aPlacement := range item.Placements {
				size = aPlacement.Size
				discoveredGiprofile = item.Giprofileid
				Ciprofileid = item.CIProfileID
				Ciengprofileid = item.CIEngProfileID
				break
			}
		}
	}
	return size, discoveredGiprofile, Ciprofileid, Ciengprofileid
}

func checkIfPodGated(pod *v1.Pod, isPodGated bool) bool {
	for _, gate := range pod.Spec.SchedulingGates {
		if gate.Name == "org.instaslice/accelarator" {
			if pod.Status.Phase == v1.PodPending && strings.Contains(pod.Status.Conditions[0].Message, "blocked") {
				isPodGated = true
			}
		}
	}
	return isPodGated
}

// podMapFunc maps pods to instaslice created allocations
func (r *InstasliceReconciler) podMapFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	instaslice := obj.(*inferencev1alpha1.Instaslice)
	for _, allocation := range instaslice.Spec.Allocations {
		if allocation.Allocationstatus == "created" || allocation.Allocationstatus == "deleted" {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: allocation.Namespace, Name: allocation.PodName}}}
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *InstasliceReconciler) SetupWithManager(mgr ctrl.Manager) error {

	restConfig := mgr.GetConfig()

	var err error
	r.kubeClient, err = kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).Named("InstaSlice-controller").
		Watches(&inferencev1alpha1.Instaslice{}, handler.EnqueueRequestsFromMapFunc(r.podMapFunc)).
		Complete(r)
}

func (r *InstasliceReconciler) unGatePod(podUpdate *v1.Pod) *v1.Pod {
	for i, gate := range podUpdate.Spec.SchedulingGates {
		if gate.Name == "org.instaslice/accelarator" {
			podUpdate.Spec.SchedulingGates = append(podUpdate.Spec.SchedulingGates[:i], podUpdate.Spec.SchedulingGates[i+1:]...)
		}
	}
	return podUpdate
}

// Policy based allocation - FirstFit
func (r *FirstFitPolicy) SetAllocationDetails(profileName string, newStart, size uint32, podUUID, nodename string,
	processed string, discoveredGiprofile int, Ciprofileid int, Ciengprofileid int,
	namespace string, podName string, gpuUuid string, resourceIdentifier string, cpuMilli int64, memory int64) *inferencev1alpha1.AllocationDetails {
	return &inferencev1alpha1.AllocationDetails{
		Profile:            profileName,
		Start:              uint32(newStart),
		Size:               uint32(size),
		PodUUID:            podUUID,
		Nodename:           nodename,
		Allocationstatus:   processed,
		Namespace:          namespace,
		PodName:            podName,
		GPUUUID:            gpuUuid,
		Resourceidentifier: resourceIdentifier,
		Cpu:                cpuMilli,
		Memory:             memory,
	}
}

// Policy based allocation - LeftToRIght
func (l *LeftToRightPolicy) SetAllocationDetails(profileName string, newStart, size uint32, podUUID, nodename string,
	processed string, discoveredGiprofile int, Ciprofileid int, Ciengprofileid int,
	namespace string, podName string, gpuUuid string) *inferencev1alpha1.AllocationDetails {
	// Implement the left-to-right policy here
	return &inferencev1alpha1.AllocationDetails{}
}

// Policy based allocation - RigghToLeft
func (l *RightToLeftPolicy) SetAllocationDetails(profileName string, newStart, size uint32, podUUID, nodename string,
	processed string, discoveredGiprofile int, Ciprofileid int, Ciengprofileid int,
	namespace string, podName string, gpuUuid string) *inferencev1alpha1.AllocationDetails {
	// Implement the left-to-right policy here
	return &inferencev1alpha1.AllocationDetails{}
}

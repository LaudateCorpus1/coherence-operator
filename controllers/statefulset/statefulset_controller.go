/*
 * Copyright (c) 2020, 2021, Oracle and/or its affiliates.
 * Licensed under the Universal Permissive License v 1.0 as shown at
 * http://oss.oracle.com/licenses/upl.
 */

package statefulset

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-lib/status"
	coh "github.com/oracle/coherence-operator/api/v1"
	"github.com/oracle/coherence-operator/controllers/reconciler"
	"github.com/oracle/coherence-operator/pkg/utils"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"os"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sort"
	"strings"
	"time"
)

const (
	// The name of this controller. This is used in events, log messages, etc.
	controllerName = "controllers.StatefulSet"

	CreateMessage        string = "successfully created StatefulSet for Coherence resource '%s'"
	FailedToScaleMessage string = "failed to scale Coherence resource %s from %d to %d due to error\n%s"
	FailedToPatchMessage string = "failed to patch Coherence resource %s due to error\n%s"

	EventReasonScale string = "Scaling"

	statusHaRetryEnv = "STATUS_HA_RETRY"
)

// blank assignment to verify that ReconcileStatefulSet implements reconcile.Reconciler.
// If the reconcile.Reconciler API was to change then we'd get a compile error here.
var _ reconcile.Reconciler = &ReconcileStatefulSet{}

var log = logf.Log.WithName(controllerName)

// NewStatefulSetReconciler returns a new StatefulSet reconciler.
func NewStatefulSetReconciler(mgr manager.Manager) reconciler.SecondaryResourceReconciler {
	// Parse the StatusHA retry time from the
	retry := time.Minute
	s := os.Getenv(statusHaRetryEnv)
	if s != "" {
		d, err := time.ParseDuration(s)
		if err == nil {
			retry = d
		} else {
			log.Info(fmt.Sprintf("Warning: The value of %s env-var is not a valid duration using default retry time", statusHaRetryEnv), "EnvVar", d.String(), "Default", retry.String())
		}
	}

	r := &ReconcileStatefulSet{
		ReconcileSecondaryResource: reconciler.ReconcileSecondaryResource{
			Kind:     coh.ResourceTypeStatefulSet,
			Template: &appsv1.StatefulSet{},
		},
		statusHARetry: retry,
	}

	r.SetCommonReconciler(controllerName, mgr)
	return r
}

type ReconcileStatefulSet struct {
	reconciler.ReconcileSecondaryResource
	statusHARetry time.Duration
}

func (in *ReconcileStatefulSet) GetReconciler() reconcile.Reconciler { return in }

// Reconcile reads that state of the Services for a deployment and makes changes based on the
// state read and the desired state based on the parent Coherence resource.
func (in *ReconcileStatefulSet) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	logger := in.GetLog().WithValues("Namespace", request.Namespace, "Name", request.Name, "Kind", "StatefulSet")
	logger.Info("Starting reconcile")

	// Attempt to lock the requested resource. If the resource is locked then another
	// request for the same resource is already in progress so requeue this one.
	if ok := in.Lock(request); !ok {
		return reconcile.Result{Requeue: true, RequeueAfter: 0}, nil
	}
	// Make sure that the request is unlocked when this method exits
	defer in.Unlock(request)

	storage, err := utils.NewStorage(request.NamespacedName, in.GetManager())
	if err != nil {
		return reconcile.Result{}, err
	}

	result, err := in.ReconcileAllResourceOfKind(ctx, request, nil, storage)
	logger.Info("Completed reconcile")
	return result, err
}

// ReconcileAllResourceOfKind will process the specified reconcile request for the specified deployment.
// The previous state being reconciled can be obtained from the storage parameter.
func (in *ReconcileStatefulSet) ReconcileAllResourceOfKind(ctx context.Context, request reconcile.Request, deployment *coh.Coherence, storage utils.Storage) (reconcile.Result, error) {
	logger := in.GetLog().WithValues("Namespace", request.Namespace, "Name", request.Name)
	logger.Info("Reconciling StatefulSet for deployment")

	result := reconcile.Result{}

	// Fetch the StatefulSet's current state
	stsCurrent, stsExists, err := in.MaybeFindStatefulSet(ctx, request.Namespace, request.Name)
	if err != nil {
		logger.Info("Finished reconciling StatefulSet for deployment. Error getting StatefulSet", "error", err.Error())
		return result, errors.Wrapf(err, "getting StatefulSet %s/%s", request.Namespace, request.Name)
	}

	if stsExists && stsCurrent.GetDeletionTimestamp() != nil {
		logger.Info("Finished reconciling StatefulSet. The StatefulSet is being deleted")
		// The StatefulSet exists but is being deleted
		return result, nil
	}

	if stsExists && deployment == nil {
		// find the owning Coherence resource
		if deployment, err = in.FindOwningCoherenceResource(ctx, stsCurrent); err != nil {
			logger.Info("Finished reconciling StatefulSet. Error finding parent Coherence resource", "error", err.Error())
			return reconcile.Result{}, err
		}
	}

	switch {
	case deployment == nil || deployment.GetReplicas() == 0:
		// The Coherence resource does not exist, or it exists and is scaling down to zero replicas
		if stsExists {
			// The StatefulSet does exist though, so it needs to be deleted.
			if deployment != nil {
				// If we get here, we must be scaling down to zero as the Coherence resource exists
				// If the Coherence resource did not exist then service suspension already happened
				// when the Coherence resource was deleted.
				logger.Info("Scaling down to zero")
				err = in.UpdateDeploymentStatusActionsState(ctx, request.NamespacedName, false)
				// TODO: what to do with error?
				if err != nil {
					logger.Info("Error updating deployment status", "error", err.Error())
				}
				if deployment.Spec.IsSuspendServicesOnShutdown() {
					// we are scaling down to zero and suspend services flag is true, so suspend services
					suspended := in.suspendServices(ctx, deployment, stsCurrent)
					switch suspended {
					case ServiceSuspendFailed:
						return reconcile.Result{Requeue: true, RequeueAfter: time.Minute}, fmt.Errorf("failed to suspend services prior to scaling down to zero")
					case ServiceSuspendSkipped:
						logger.Info("Skipping suspension of Coherence services prior to deletion of StatefulSet")
					case ServiceSuspendSuccessful:
					}
				}
			}
			// delete the StatefulSet
			err = in.Delete(ctx, request.Namespace, request.Name, logger)
		} else {
			// The StatefulSet and parent resource has been deleted so no more to do
			_, err = in.UpdateDeploymentStatus(ctx, request)
			return reconcile.Result{}, err
		}
	case !stsExists:
		// StatefulSet does not exist but deployment does so create the StatefulSet (checking any start quorum)
		result, err = in.createStatefulSet(ctx, deployment, storage, logger)
	default:
		// Both StatefulSet and deployment exists so this is maybe an update
		result, err = in.updateStatefulSet(ctx, deployment, stsCurrent, storage, logger)
	}

	var updated *coh.Coherence
	if err == nil {
		if updated, err = in.UpdateDeploymentStatus(ctx, request); err == nil {
			if updated.Status.Phase == coh.ConditionTypeReady && !updated.Status.ActionsExecuted && deployment.GetReplicas() != 0  {
				in.execActions(ctx, stsCurrent, deployment)
				err = in.UpdateDeploymentStatusActionsState(ctx, request.NamespacedName, true)
			}
		}
	}

	if err != nil {
		logger.Info("Finished reconciling StatefulSet with error", "error", err.Error())
		return result, err
	}

	logger.Info("Finished reconciling StatefulSet for deployment")
	return result, nil
}

// UpdateDeploymentStatusActionsState updates the Coherence resource's status ActionsExecuted flag.
func (in *ReconcileStatefulSet) UpdateDeploymentStatusActionsState(ctx context.Context, key types.NamespacedName, actionExecuted bool) error {
	deployment := &coh.Coherence{}
	err := in.GetClient().Get(ctx, key, deployment)
	switch {
	case err != nil && apierrors.IsNotFound(err):
		// deployment not found - possibly deleted
		err = nil
	case err != nil:
		// an error occurred
		err = errors.Wrapf(err, "getting deployment %s", key.Name)
	case deployment.GetDeletionTimestamp() != nil:
		// deployment is being deleted
		err = nil
	default:
		if deployment.Status.ActionsExecuted != actionExecuted {
			updated := deployment.DeepCopy()
			updated.Status.ActionsExecuted = actionExecuted
			patch, err := in.CreateTwoWayPatchOfType(types.MergePatchType, deployment.Name, updated, deployment)
			if err != nil {
				return errors.Wrap(err, "creating Coherence resource status patch")
			}
			if patch != nil {
				err = in.GetClient().Status().Patch(ctx, deployment, patch)
				if err != nil {
					return errors.Wrap(err, "updating Coherence resource status")
				}
			}
		}
	}
	return err
}

// execActions executes actions
func (in *ReconcileStatefulSet) execActions(ctx context.Context, sts *appsv1.StatefulSet, deployment *coh.Coherence) {
	coherenceProbe := CoherenceProbe{
		Client: in.GetClient(),
		Config: in.GetManager().GetConfig(),
	}

	for _, action := range deployment.Spec.Actions {
		if action.Probe != nil {
			if ok := coherenceProbe.ExecuteProbe(ctx, deployment, sts, action.Probe); !ok {
				log.Info("Action probe execution failed.", "probe", action.Probe)
			}
		}
		if action.Job != nil {
			job := buildActionJob(action.Name, action.Job, deployment)
			if err := in.GetClient().Create(ctx, job); err != nil {
				log.Info("Action job creation failed", "error", err.Error())
			} else {
				log.Info(fmt.Sprintf("Created action job '%s'", job.Name))
			}
		}
	}
}

// buildActionJob creates job based on ActionJob config
func buildActionJob(actionName string, actionJob *coh.ActionJob, deployment *coh.Coherence) *batchv1.Job {
	generateName := deployment.Name + "-"
	if actionName := strings.TrimSpace(actionName); actionName != "" {
		generateName = generateName + actionName + "-"
	}
	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Job",
			APIVersion: "batch/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels:      actionJob.Labels,
			Annotations: actionJob.Annotations,
			GenerateName: generateName,
			Namespace: deployment.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         deployment.APIVersion,
					Kind:               deployment.Kind,
					Name:               deployment.Name,
					UID:                deployment.UID,
					Controller:         pointer.BoolPtr(true),
					BlockOwnerDeletion: pointer.BoolPtr(false),
				},
			},
		},
		Spec: actionJob.Spec,
	}
	return job
}

func (in *ReconcileStatefulSet) createStatefulSet(ctx context.Context, deployment *coh.Coherence, storage utils.Storage, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Creating StatefulSet")

	ok, reason := in.canCreate(ctx, deployment)

	if !ok {
		// start quorum not met, send event and update deployment status
		in.GetEventRecorder().Event(deployment, corev1.EventTypeNormal, "Waiting", reason)
		_ = in.UpdateDeploymentStatusCondition(ctx, deployment.GetNamespacedName(), status.Condition{
			Type:    coh.ConditionTypeWaiting,
			Status:  corev1.ConditionTrue,
			Reason:  "StatusQuorum",
			Message: reason,
		})
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
	}

	err := in.Create(ctx, deployment.Name, storage, logger)
	if err == nil {
		// ensure that the deployment has a Created status
		err := in.UpdateDeploymentStatusPhase(ctx, deployment.GetNamespacedName(), coh.ConditionTypeCreated)
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "updating deployment status")
		}

		// send a successful creation event
		msg := fmt.Sprintf(CreateMessage, deployment.Name)
		in.GetEventRecorder().Event(deployment, corev1.EventTypeNormal, reconciler.EventReasonCreated, msg)
	}

	logger.Info("Created statefulSet")
	return reconcile.Result{}, err
}

// canCreate determines whether any specified start quorum has been met.
func (in *ReconcileStatefulSet) canCreate(ctx context.Context, deployment *coh.Coherence) (bool, string) {
	if deployment.Spec.StartQuorum == nil || len(deployment.Spec.StartQuorum) == 0 {
		// there is no start quorum
		return true, ""
	}

	logger := in.GetLog().WithValues("Namespace", deployment.Namespace, "Name", deployment.Name)
	logger.Info("Checking deployment start quorum")

	var quorum []string

	for _, q := range deployment.Spec.StartQuorum {
		if q.Deployment == "" {
			// this start-quorum does not have a dependency name so skip it
			continue
		}
		// work out which Namespace to look for the dependency in
		var namespace string
		if q.Namespace == "" {
			// start-quorum does not specify a namespace so use the same one as the deployment
			namespace = deployment.Namespace
		} else {
			// start-quorum does specify a namespace so use it
			namespace = q.Namespace
		}
		dep, found, err := in.MaybeFindDeployment(ctx, namespace, q.Deployment)
		switch {
		case err != nil:
			// cannot create due to an error looking up the deployment
			quorum = append(quorum, fmt.Sprintf("error finding deployment '%s' - %s", q.Deployment, err.Error()))
		case !found:
			// cannot create as the deployment does not yet exist
			quorum = append(quorum, fmt.Sprintf("deployment '%s/%s' does not exist", namespace, q.Deployment))
		case found && q.PodCount > 0 && dep.Status.ReadyReplicas < q.PodCount:
			// deployment exists and quorum requires a specific number of ready Pods
			quorum = append(quorum, fmt.Sprintf("role '%s/%s' to have %d ready Pods (ready=%d)", namespace, q.Deployment, q.PodCount, dep.Status.ReadyReplicas))
		case found && dep.Status.Phase != coh.ConditionTypeReady:
			// deployment exists and quorum requires all pods ready
			quorum = append(quorum, fmt.Sprintf("deployment '%s' is not ready", q.Deployment))
		}
	}

	if len(quorum) > 0 {
		reason := "Waiting for start quorum to be met: \"" + strings.Join(quorum, "\" and \"") + "\""
		logger.Info(reason)
		return false, reason
	}
	return true, ""
}

func (in *ReconcileStatefulSet) updateStatefulSet(ctx context.Context, deployment *coh.Coherence, current *appsv1.StatefulSet, storage utils.Storage, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Updating statefulSet")

	var err error
	result := reconcile.Result{}

	// get the desired resource state from the store
	resource, found := storage.GetLatest().GetResource(coh.ResourceTypeStatefulSet, current.Name)
	if !found {
		// Desired state not found requeue and the request should sort itself out next time around
		logger.Info("Cannot locate desired state for StatefulSet, possibly a deletion, re-queuing request")
		return reconcile.Result{Requeue: true}, nil
	}
	if resource.IsDelete() {
		// we should never get here, requeue and the request should sort itself out next time around
		logger.Info("In update path for StatefulSet, but is a deletion - re-queuing request")
		return reconcile.Result{Requeue: true}, nil
	}

	desired := resource.Spec.(*appsv1.StatefulSet)
	desiredReplicas := in.getReplicas(desired)
	currentReplicas := in.getReplicas(current)

	switch {
	case currentReplicas < desiredReplicas:
		// scale up - if also updating we do the rolling upgrade first followed by the
		// scale up so we do not do a rolling upgrade of the bigger scaled up cluster

		// try the patch first
		result, err = in.patchStatefulSet(ctx, deployment, current, desired, storage, logger)
		if err == nil && !result.Requeue {
			// there was nothing else to patch so we can do the scale up
			result, err = in.scale(ctx, deployment, current, currentReplicas, desiredReplicas)
		}
	case currentReplicas > desiredReplicas:
		// scale down - if also updating we scale down followed by a rolling upgrade so that
		// we do the rolling upgrade on the smaller scaled down cluster.

		// do the scale down
		_, err = in.scale(ctx, deployment, current, currentReplicas, desiredReplicas)
		// requeue the request so we do any upgrade next time around
		result.Requeue = true
	default:
		// just an update
		result, err = in.patchStatefulSet(ctx, deployment, current, desired, storage, logger)
	}

	return result, err
}

// Patch the StatefulSet if required, returning a bool to indicate whether a patch was applied.
func (in *ReconcileStatefulSet) patchStatefulSet(ctx context.Context, deployment *coh.Coherence, current, desired *appsv1.StatefulSet, storage utils.Storage, logger logr.Logger) (reconcile.Result, error) {
	hashMatches := in.HashLabelsMatch(current, storage)
	resource, _ := storage.GetPrevious().GetResource(coh.ResourceTypeStatefulSet, current.GetName())
	original := &appsv1.StatefulSet{}

	// save the replica counts before we remove the status
	readyReplicas := current.Status.ReadyReplicas
	currentReplicas := in.getReplicas(current)

	if resource.IsPresent() {
		err := resource.As(original)
		if err != nil {
			return in.HandleErrAndRequeue(ctx, err, deployment, fmt.Sprintf(FailedToPatchMessage, deployment.Name, err.Error()), logger)
		}
	} else {
		// there was no previous
		original = desired
	}

	// We NEVER change the replicas or Status in an update.
	// Replicas is handled by scaling so we always set the desired replicas to match the current replicas
	desired.Spec.Replicas = current.Spec.Replicas
	original.Spec.Replicas = current.Spec.Replicas

	// We NEVER patch finalizers
	original.ObjectMeta.Finalizers = current.ObjectMeta.Finalizers
	desired.ObjectMeta.Finalizers = current.ObjectMeta.Finalizers

	// We need to ensure we do not create a patch due to differences in
	// StatefulSet Status so we blank out the status fields
	desired.Status = appsv1.StatefulSetStatus{}
	current.Status = appsv1.StatefulSetStatus{}
	original.Status = appsv1.StatefulSetStatus{}

	// The VolumeClaimTemplates of a StatefulSet cannot be changed so blank them out for the patch
	// The validation web-hook should have rejected any invalid updates but this ensures that
	// we do not try to patch PV claims
	desired.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{}
	current.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{}
	original.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{}

	// ensure we do not patch any fields that may be set by a previous version of the Operator
	// as this will cause a rolling update of the Pods, typically these are fields where
	// the Operator sets defaults and we changed the default behaviour
	in.blankContainerFields(deployment, desired)
	in.blankContainerFields(deployment, current)
	in.blankContainerFields(deployment, original)

	// Sort the environment variables so we do not patch on just a re-ordering of env vars
	in.sortEnvForAllContainers(desired)
	in.sortEnvForAllContainers(current)
	in.sortEnvForAllContainers(original)

	// ensure the Coherence image is present so that we do not patch on a Coherence resource
	// from pre-3.1.x that does not have images set
	if deployment.Spec.Image == nil {
		cohImage := in.getCoherenceImage(desired)
		in.setCoherenceImage(original, cohImage)
		in.setCoherenceImage(current, cohImage)
	}

	// ensure the utils image is present so that we do not patch on a Coherence resource
	// from pre-3.1.x that does not have images set
	if deployment.Spec.CoherenceUtils == nil || deployment.Spec.CoherenceUtils.Image == nil {
		utilsImage := in.getUtilsImage(desired)
		in.setUtilsImage(original, utilsImage)
		in.setUtilsImage(current, utilsImage)
	}

	// a callback function that the 3-way patch method will call just before it applies a patch
	// if there is any patch to apply, this will check StatusHA if required and update the deployment status
	callback := func() {
		// ensure that the deployment has an "Upgrading" status
		if err := in.UpdateDeploymentStatusPhase(ctx, deployment.GetNamespacedName(), coh.ConditionTypeRollingUpgrade); err != nil {
			logger.Error(err, "Error updating deployment status to Upgrading")
		}
	}

	// fix the CreationTimestamp so that it is not in the patch
	desired.SetCreationTimestamp(current.GetCreationTimestamp())
	// create the patch to see whether there is anything to update
	patch, data, err := in.CreateThreeWayPatch(current.GetName(), original, desired, current, reconciler.PatchIgnore)
	if err != nil {
		return reconcile.Result{}, errors.Wrapf(err, "failed to create patch for StatefulSet/%s", current.GetName())
	}

	if patch == nil {
		// nothing to patch so just return
		return reconcile.Result{}, nil
	}

	logger.Info("Updating StatefulSet")

	if deployment.Spec.CheckHABeforeUpdate() {
		// Check we have the expected number of ready replicas
		if readyReplicas != currentReplicas {
			logger.Info("Re-queuing update request. StatefulSet Status not all replicas are ready", "Ready", readyReplicas, "CurrentReplicas", currentReplicas)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Minute}, nil
		}

		// perform the StatusHA check...
		checker := CoherenceProbe{Client: in.GetClient(), Config: in.GetManager().GetConfig()}
		ha := checker.IsStatusHA(ctx, deployment, current)
		if !ha {
			logger.Info("Coherence cluster is not StatusHA - re-queuing update request.")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Minute}, nil
		}
	} else {
		// the user specifically set a forced update!
		logger.V(0).Info("WARNING - Updating StatefulSet without a StatusHA test, update was forced")
	}

	// if there is only a single replica we need to do service suspension before update
	if current.Status.ReadyReplicas == 1 {
		suspended := in.suspendServices(ctx, deployment, current)
		switch suspended {
		case ServiceSuspendFailed:
			return reconcile.Result{Requeue: true, RequeueAfter: time.Minute}, fmt.Errorf("failed to suspend services prior to updating single member deployment")
		case ServiceSuspendSkipped:
			logger.Info("Skipping suspension of Coherence services in single member deployment " + deployment.Name +
				" prior to update StatefulSet")
		case ServiceSuspendSuccessful:
		}
	}

	// now apply the patch
	patched, err := in.ApplyThreeWayPatchWithCallback(ctx, current.GetName(), current, patch, data, callback)

	// log the result of patching
	switch {
	case err != nil:
		logger.Info("Error patching StatefulSet " + err.Error())
		return in.HandleErrAndRequeue(ctx, err, deployment, fmt.Sprintf(FailedToPatchMessage, deployment.Name, err.Error()), logger)
	case !patched:
		return reconcile.Result{Requeue: true}, nil
	}

	if patched && hashMatches {
		logger.Info("Patch applied to statefulSet even though hashes matched (possible external update)")
	}

	return reconcile.Result{}, nil
}

// suspendServices suspends Coherence services in the target deployment.
func (in *ReconcileStatefulSet) suspendServices(ctx context.Context, deployment *coh.Coherence, current *appsv1.StatefulSet) ServiceSuspendStatus {
	probe := CoherenceProbe{
		Client: in.GetClient(),
		Config: in.GetManager().GetConfig(),
	}
	return probe.SuspendServices(ctx, deployment, current)
}

func (in *ReconcileStatefulSet) sortEnvForAllContainers(sts *appsv1.StatefulSet) {
	for i := range sts.Spec.Template.Spec.InitContainers {
		c := sts.Spec.Template.Spec.InitContainers[i]
		in.sortEnvForContainer(&c)
		sts.Spec.Template.Spec.InitContainers[i] = c
	}
	for i := range sts.Spec.Template.Spec.Containers {
		c := sts.Spec.Template.Spec.Containers[i]
		in.sortEnvForContainer(&c)
		sts.Spec.Template.Spec.Containers[i] = c
	}
}

func (in *ReconcileStatefulSet) sortEnvForContainer(c *corev1.Container) {
	sort.Slice(c.Env, func(i, j int) bool {
		return c.Env[i].Name < c.Env[j].Name
	})
}

func (in *ReconcileStatefulSet) getUtilsImage(sts *appsv1.StatefulSet) string {
	for i := range sts.Spec.Template.Spec.InitContainers {
		c := sts.Spec.Template.Spec.InitContainers[i]
		if c.Name == coh.ContainerNameUtils {
			return c.Image
		}
	}
	return ""
}

func (in *ReconcileStatefulSet) setUtilsImage(sts *appsv1.StatefulSet, image string) {
	for i := range sts.Spec.Template.Spec.InitContainers {
		c := sts.Spec.Template.Spec.InitContainers[i]
		if c.Name == coh.ContainerNameUtils {
			c.Image = image
			sts.Spec.Template.Spec.InitContainers[i] = c
		}
	}
}

func (in *ReconcileStatefulSet) getCoherenceImage(sts *appsv1.StatefulSet) string {
	for i := range sts.Spec.Template.Spec.Containers {
		c := sts.Spec.Template.Spec.Containers[i]
		if c.Name == coh.ContainerNameCoherence {
			return c.Image
		}
	}
	return ""
}

func (in *ReconcileStatefulSet) setCoherenceImage(sts *appsv1.StatefulSet, image string) {
	for i := range sts.Spec.Template.Spec.Containers {
		c := sts.Spec.Template.Spec.Containers[i]
		if c.Name == coh.ContainerNameCoherence {
			c.Image = image
			sts.Spec.Template.Spec.Containers[i] = c
		}
	}
}

// Blank out any fields that we do not want to include in the patch
// Typically these are fields where we changed the default behaviour in the newer Operator versions
func (in *ReconcileStatefulSet) blankContainerFields(deployment *coh.Coherence, sts *appsv1.StatefulSet) {
	if deployment.Spec.Affinity == nil {
		// affinity not set by user so do not diff on it
		sts.Spec.Template.Spec.Affinity = nil
	}
	in.blankUtilsContainerFields(sts)
	in.blankCoherenceContainerFields(sts)
}

// Blanks out any fields that may have been set by a previous Operator version.
// DO NOT blank out anything that the user has control over as they may have
// updated them so we need to include them in the patch
func (in *ReconcileStatefulSet) blankUtilsContainerFields(sts *appsv1.StatefulSet) {
	for i := range sts.Spec.Template.Spec.InitContainers {
		c := sts.Spec.Template.Spec.InitContainers[i]
		if c.Name == coh.ContainerNameUtils {
			// This is the Utils Container
			// blank out the container command field
			c.Command = []string{}
			// set the updated init-container back into the StatefulSet
			sts.Spec.Template.Spec.InitContainers[i] = c
		}
	}
}

// Blanks out any fields that may have been set by a previous Operator version.
// DO NOT blank out anything that the user has control over as they may have
// updated them so we need to include them in the patch
func (in *ReconcileStatefulSet) blankCoherenceContainerFields(sts *appsv1.StatefulSet) {
	for i := range sts.Spec.Template.Spec.Containers {
		c := sts.Spec.Template.Spec.Containers[i]
		if c.Name == coh.ContainerNameCoherence {
			// This is the Coherence Container
			// blank out the container command field
			c.Command = []string{}
			// blank the WKA env var
			for e := range c.Env {
				ev := c.Env[e]
				if ev.Name == coh.EnvVarCohWka {
					ev.Value = ""
					c.Env[e] = ev
				}
			}
			// set the updated container back into the StatefulSet
			sts.Spec.Template.Spec.Containers[i] = c
		}
	}
}

// Scale will scale a StatefulSet up or down
func (in *ReconcileStatefulSet) scale(ctx context.Context, deployment *coh.Coherence, sts *appsv1.StatefulSet, current, desired int32) (reconcile.Result, error) {
	// if the StatefulSet is not stable we cannot scale (e.g. it might already be in the middle of a rolling upgrade)
	logger := in.GetLog().WithValues("Namespace", deployment.Name, "Name", deployment.Name)
	logger.Info("Scaling StatefulSet", "Current", current, "Desired", desired)

	policy := deployment.Spec.GetEffectiveScalingPolicy()

	// ensure that the deployment has a Scaling status
	if err := in.UpdateDeploymentStatusPhase(ctx, deployment.GetNamespacedName(), coh.ConditionTypeScaling); err != nil {
		logger.Error(err, "Error updating deployment status to Scaling")
	}

	switch policy {
	case coh.SafeScaling:
		return in.safeScale(ctx, deployment, sts, desired, current)
	case coh.ParallelScaling:
		return in.parallelScale(ctx, deployment, sts, desired)
	case coh.ParallelUpSafeDownScaling:
		if desired > current {
			return in.parallelScale(ctx, deployment, sts, desired)
		}
		return in.safeScale(ctx, deployment, sts, desired, current)
	default:
		// shouldn't get here, but better safe than sorry
		return in.safeScale(ctx, deployment, sts, desired, current)
	}
}

// A nil safe method to get the number of replicas for a StatefulSet
func (in *ReconcileStatefulSet) getReplicas(sts *appsv1.StatefulSet) int32 {
	if sts == nil || sts.Spec.Replicas == nil {
		return 1
	}
	return *sts.Spec.Replicas
}

// safeScale will scale a StatefulSet up or down by one and requeue the request.
func (in *ReconcileStatefulSet) safeScale(ctx context.Context, deployment *coh.Coherence, sts *appsv1.StatefulSet, desired int32, current int32) (reconcile.Result, error) {
	logger := in.GetLog().WithValues("Namespace", deployment.Name, "Name", deployment.Name)
	logger.Info("Safe scaling StatefulSet", "Current", current, "Desired", desired)

	if sts.Status.ReadyReplicas != current {
		logger.Info("Coherence cluster is not StatusHA - Re-queuing scaling request. Stateful set not ready", "Ready", sts.Status.ReadyReplicas, "Replicas", current)
	}

	checker := CoherenceProbe{Client: in.GetClient(), Config: in.GetManager().GetConfig()}
	ha := current == 1 || checker.IsStatusHA(ctx, deployment, sts)

	if ha {
		var replicas int32

		if desired > current {
			replicas = current + 1
		} else {
			replicas = current - 1
		}

		logger.Info("Coherence cluster is StatusHA, safely scaling", "Current", current, "Replicas", replicas, "Desired", desired)

		// use the parallel method to just scale by one
		_, err := in.parallelScale(ctx, deployment, sts, replicas)
		if err == nil {
			if replicas == desired {
				// we're at the desired size so finished scaling
				return reconcile.Result{Requeue: false}, nil
			}
			// scaled by one but not yet at the desired size - requeue request after one minute
			return reconcile.Result{Requeue: true, RequeueAfter: time.Minute}, nil
		}
		// failed
		return in.HandleErrAndRequeue(ctx, err, deployment, fmt.Sprintf(FailedToScaleMessage, deployment.Name, current, replicas, err.Error()), logger)
	}

	// Not StatusHA - wait one minute
	logger.Info("Coherence cluster is not StatusHA - Re-queuing scaling request")
	return reconcile.Result{Requeue: true, RequeueAfter: in.statusHARetry}, nil
}

// parallelScale will scale the StatefulSet by the required amount in one request.
func (in *ReconcileStatefulSet) parallelScale(ctx context.Context, deployment *coh.Coherence, sts *appsv1.StatefulSet, replicas int32) (reconcile.Result, error) {
	logger := in.GetLog().WithValues("Namespace", deployment.Name, "Name", deployment.Name)
	logger.Info("Scaling StatefulSet", "Replicas", replicas)

	events := in.GetEventRecorder()

	// Update this Coherence resource's status
	deployment.Status.Phase = coh.ConditionTypeScaling
	deployment.Status.Replicas = replicas

	if err := in.UpdateDeploymentStatusPhase(ctx, deployment.GetNamespacedName(), coh.ConditionTypeScaling); err != nil {
		logger.Error(err, "Error updating deployment status to Scaling")
	}

	// Create the desired state
	stsDesired := &appsv1.StatefulSet{}
	sts.DeepCopyInto(stsDesired)
	stsDesired.Spec.Replicas = &replicas

	// ThreeWayPatch theStatefulSet to trigger it to scale
	_, err := in.ThreeWayPatch(ctx, sts.Name, sts, sts, stsDesired)
	if err != nil {
		// send a failed scale event
		msg := fmt.Sprintf("failed to scale StatefulSet %s from %d to %d", sts.Name, in.getReplicas(sts), replicas)
		events.Event(deployment, corev1.EventTypeNormal, EventReasonScale, msg)
		return reconcile.Result{}, errors.Wrap(err, msg)
	}

	// send a successful scale event
	msg := fmt.Sprintf("scaled StatefulSet %s from %d to %d", sts.Name, in.getReplicas(sts), replicas)
	events.Event(deployment, corev1.EventTypeNormal, EventReasonScale, msg)

	return reconcile.Result{}, nil
}

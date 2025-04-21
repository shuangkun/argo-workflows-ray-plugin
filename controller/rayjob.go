package controller

import (
	"encoding/json"
	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	executorplugins "github.com/argoproj/argo-workflows/v3/pkg/plugins/executor"
	"github.com/gin-gonic/gin"
	rayjob "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"
	rayversioned "github.com/ray-project/kuberay/ray-operator/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"net/http"
	"time"
)

const (
	LabelKeyWorkflow string = "workflows.argoproj.io/workflow"
)

type RayJobController struct {
	RayClient *rayversioned.Clientset
}

type RayPluginBody struct {
	RayJob *rayjob.RayJob `json:"ray"`
}

func (ct *RayJobController) ExecuteRayJob(ctx *gin.Context) {
	c := &executorplugins.ExecuteTemplateArgs{}
	err := ctx.BindJSON(&c)
	if err != nil {
		klog.Error(err)
		return
	}

	inputBody := &RayPluginBody{
		RayJob: &rayjob.RayJob{},
	}

	pluginJson, _ := c.Template.Plugin.MarshalJSON()
	klog.Info("Receive: ", string(pluginJson))
	err = json.Unmarshal(pluginJson, &inputBody)
	if err != nil {
		klog.Error(err)
		ct.Response404(ctx)
		return
	}

	job := inputBody.RayJob

	if job.Name == "" {
		job.ObjectMeta.Name = c.Workflow.ObjectMeta.Name
	}

	if job.ObjectMeta.Namespace == "" {
		job.Namespace = "default"
	}

	var exists = false

	// 1. query job exists
	existsJob, err := ct.RayClient.RayV1().RayJobs(job.Namespace).Get(ctx, job.Name, metav1.GetOptions{})
	if err != nil {
		exists = false
	} else {
		exists = true
	}
	// 2. found and return
	if exists {
		klog.Info("# found exists Ray Job: ", job.Name, "returning Status...", job.Status)
		ct.ResponseRayJob(ctx, existsJob)
		return
	}

	// 3.Label keys with workflow Name
	InjectRayJobWithWorkflowName(job, c.Workflow.ObjectMeta.Name)

	newJob, err := ct.RayClient.RayV1().RayJobs(job.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		klog.Error("### " + err.Error())
		ct.ResponseMsg(ctx, wfv1.NodeFailed, err.Error())
		return
	}

	ct.ResponseCreated(ctx, newJob)

}

func (ct *RayJobController) ResponseCreated(ctx *gin.Context, job *rayjob.RayJob) {
	message := job.Status.Message
	ctx.JSON(http.StatusOK, &executorplugins.ExecuteTemplateReply{
		Node: &wfv1.NodeResult{
			Phase:   wfv1.NodePending,
			Message: message,
			Outputs: nil,
		},
		Requeue: &metav1.Duration{
			Duration: 10 * time.Second,
		},
	})
}

func (ct *RayJobController) ResponseMsg(ctx *gin.Context, status wfv1.NodePhase, msg string) {
	ctx.JSON(http.StatusOK, &executorplugins.ExecuteTemplateReply{
		Node: &wfv1.NodeResult{
			Phase:   status,
			Message: msg,
			Outputs: nil,
		},
	})
}

func (ct *RayJobController) ResponseRayJob(ctx *gin.Context, job *rayjob.RayJob) {
	jobPhase := &job.Status.JobStatus
	var status wfv1.NodePhase
	switch *jobPhase {
	case rayjob.JobStatusRunning:
		status = wfv1.NodeRunning
	case rayjob.JobStatusSucceeded:
		status = wfv1.NodeSucceeded
	case rayjob.JobStatusPending:
		status = wfv1.NodePending
	case rayjob.JobStatusFailed:
		status = wfv1.NodeFailed
	default:
		status = wfv1.NodeRunning
	}

	var requeue *metav1.Duration
	if status == wfv1.NodeRunning || status == wfv1.NodePending {
		requeue = &metav1.Duration{
			Duration: 10 * time.Second,
		}
	} else {
		requeue = nil
	}
	succeed := int32(0)
	total := 1
	if job.Status.StartTime != nil {
		if status == wfv1.NodeSucceeded {
			succeed = 1
		}
	}
	progress, _ := wfv1.NewProgress(int64(succeed), int64(total))
	klog.Infof("### Job %v Phase "+", status: %v", job.Name, status)
	message := job.Status.Message
	ctx.JSON(http.StatusOK, &executorplugins.ExecuteTemplateReply{
		Node: &wfv1.NodeResult{
			Phase:    status,
			Message:  message,
			Outputs:  nil,
			Progress: progress,
		},
		Requeue: requeue,
	})
}

func (ct *RayJobController) Response404(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusNotFound)
}

func InjectRayJobWithWorkflowName(job *rayjob.RayJob, workflowName string) {
	headGroupSpec := job.Spec.RayClusterSpec.HeadGroupSpec
	if headGroupSpec.Template.ObjectMeta.Labels != nil {
		headGroupSpec.Template.ObjectMeta.Labels[LabelKeyWorkflow] = workflowName
	} else {
		headGroupSpec.Template.ObjectMeta.Labels = map[string]string{
			LabelKeyWorkflow: workflowName,
		}
	}
	// 深拷贝 workerGroupSpecs，避免对原始数据的意外修改
	workerGroupSpecs := make([]rayjob.WorkerGroupSpec, len(job.Spec.RayClusterSpec.WorkerGroupSpecs))
	copy(workerGroupSpecs, job.Spec.RayClusterSpec.WorkerGroupSpecs)

	for i := range workerGroupSpecs {
		labels := workerGroupSpecs[i].Template.ObjectMeta.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		labels[LabelKeyWorkflow] = workflowName
		workerGroupSpecs[i].Template.ObjectMeta.Labels = labels
	}

	job.Spec.RayClusterSpec.HeadGroupSpec = headGroupSpec
	job.Spec.RayClusterSpec.WorkerGroupSpecs = workerGroupSpecs
}

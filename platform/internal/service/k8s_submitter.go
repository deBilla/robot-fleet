package service

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// K8sJobSubmitter submits training jobs as Kubernetes Jobs via kubectl.
// In production, this would use client-go or the Kubeflow Pipelines API.
type K8sJobSubmitter struct {
	TrainingImage string
	S3Endpoint    string
	S3Bucket      string
	S3AccessKey   string
	S3SecretKey   string
	CallbackURL   string
	Namespace     string
}

// NewK8sJobSubmitter creates a job submitter with S3 and callback configuration.
func NewK8sJobSubmitter(trainingImage, s3Endpoint, s3Bucket, s3AccessKey, s3SecretKey, callbackURL, namespace string) *K8sJobSubmitter {
	if namespace == "" {
		namespace = "default"
	}
	return &K8sJobSubmitter{
		TrainingImage: trainingImage,
		S3Endpoint:    s3Endpoint,
		S3Bucket:      s3Bucket,
		S3AccessKey:   s3AccessKey,
		S3SecretKey:   s3SecretKey,
		CallbackURL:   callbackURL,
		Namespace:     namespace,
	}
}

func (k *K8sJobSubmitter) SubmitTrainingJob(ctx context.Context, job *store.TrainingJobRecord) error {
	manifest := k.buildTrainingJobManifest(job)
	return k.applyManifest(ctx, manifest, job.ID)
}

func (k *K8sJobSubmitter) SubmitEvalJob(ctx context.Context, eval *store.TrainingEvalRecord, artifactURL string) error {
	manifest := k.buildEvalJobManifest(eval, artifactURL)
	return k.applyManifest(ctx, manifest, eval.ID)
}

func (k *K8sJobSubmitter) buildTrainingJobManifest(job *store.TrainingJobRecord) string {
	callbackURL := k.CallbackURL
	if callbackURL != "" && !strings.HasSuffix(callbackURL, "/") {
		callbackURL += "/training/callback"
	}

	return fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: training-%s
  namespace: %s
  labels:
    app: fleetos-training
    job-id: "%s"
    tenant-id: "%s"
spec:
  backoffLimit: 1
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: trainer
        image: %s
        command: ["python", "train_locomotion.py"]
        args:
        - "--job-id=%s"
        - "--timesteps=%d"
        - "--device=%s"
        - "--s3-endpoint=%s"
        - "--s3-bucket=%s"
        - "--s3-access-key=%s"
        - "--s3-secret-key=%s"
        - "--callback-url=%s"
        resources:
          requests:
            cpu: "2"
            memory: "4Gi"
          limits:
            cpu: "4"
            memory: "8Gi"
`, job.ID, k.Namespace, job.ID, job.TenantID,
		k.TrainingImage,
		job.ID, job.Timesteps, job.Device,
		k.S3Endpoint, k.S3Bucket, k.S3AccessKey, k.S3SecretKey,
		callbackURL)
}

func (k *K8sJobSubmitter) buildEvalJobManifest(eval *store.TrainingEvalRecord, artifactURL string) string {
	callbackURL := k.CallbackURL
	if callbackURL != "" && !strings.HasSuffix(callbackURL, "/") {
		callbackURL += "/eval/callback"
	}

	modelPath := artifactURL
	if modelPath == "" {
		modelPath = fmt.Sprintf("training/%s/policy.zip", eval.JobID)
	}

	return fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: eval-%s
  namespace: %s
  labels:
    app: fleetos-eval
    eval-id: "%s"
    job-id: "%s"
spec:
  backoffLimit: 1
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: evaluator
        image: %s
        command: ["python", "evaluate_policy.py"]
        args:
        - "--eval-id=%s"
        - "--model-path=%s"
        - "--scenarios=%d"
        - "--s3-endpoint=%s"
        - "--s3-bucket=%s"
        - "--s3-access-key=%s"
        - "--s3-secret-key=%s"
        - "--callback-url=%s"
        resources:
          requests:
            cpu: "1"
            memory: "2Gi"
          limits:
            cpu: "2"
            memory: "4Gi"
`, eval.ID, k.Namespace, eval.ID, eval.JobID,
		k.TrainingImage,
		eval.ID, modelPath, eval.ScenariosTotal,
		k.S3Endpoint, k.S3Bucket, k.S3AccessKey, k.S3SecretKey,
		callbackURL)
}

func (k *K8sJobSubmitter) applyManifest(ctx context.Context, manifest, jobID string) error {
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("kubectl apply failed", "job", jobID, "error", err, "output", string(output))
		return fmt.Errorf("submit k8s job %s: %w", jobID, err)
	}
	slog.Info("k8s job submitted", "job", jobID)
	return nil
}

// NoOpSubmitter is a stub submitter for local development without Kubernetes.
type NoOpSubmitter struct{}

func (n *NoOpSubmitter) SubmitTrainingJob(_ context.Context, job *store.TrainingJobRecord) error {
	slog.Info("noop: training job queued (no k8s)", "job", job.ID)
	return nil
}

func (n *NoOpSubmitter) SubmitEvalJob(_ context.Context, eval *store.TrainingEvalRecord, _ string) error {
	slog.Info("noop: eval job queued (no k8s)", "eval", eval.ID)
	return nil
}

package controllers

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/github/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func newGithubClient(server *httptest.Server) *github.Client {
	c := github.Config{
		Token: "token",
	}
	client, err := c.NewClient()
	if err != nil {
		panic(err)
	}

	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		panic(err)
	}
	client.Client.BaseURL = baseURL

	return client
}

func TestDetermineDesiredReplicas_RepositoryRunner(t *testing.T) {
	intPtr := func(v int) *int {
		return &v
	}

	metav1Now := metav1.Now()
	testcases := []struct {
		description string

		repo   string
		org    string
		labels []string

		fixed     *int
		max       *int
		min       *int
		sReplicas *int
		sTime     *metav1.Time

		workflowRuns             string
		workflowRuns_queued      string
		workflowRuns_in_progress string

		workflowJobs map[int]string
		want         int
		err          string
	}{
		// Legacy functionality
		// 3 demanded, max at 3
		{
			repo:                     "test/valid",
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}]}"`,
			want:                     3,
		},
		// Explicitly speified the default `self-hosted` label which is ignored by the simulator,
		// as we assume that GitHub Actions automatically associates the `self-hosted` label to every self-hosted runner.
		// 3 demanded, max at 3
		{
			repo:                     "test/valid",
			labels:                   []string{"self-hosted"},
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}]}"`,
			want:                     3,
		},
		// 2 demanded, max at 3, currently 3, delay scaling down due to grace period
		{
			repo:                     "test/valid",
			min:                      intPtr(2),
			max:                      intPtr(3),
			sReplicas:                intPtr(3),
			sTime:                    &metav1Now,
			workflowRuns:             `{"total_count": 3, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			want:                     3,
		},
		// 3 demanded, max at 2
		{
			repo:                     "test/valid",
			min:                      intPtr(2),
			max:                      intPtr(2),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}]}"`,
			want:                     2,
		},
		// 2 demanded, min at 2
		{
			repo:                     "test/valid",
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 3, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			want:                     2,
		},
		// 1 demanded, min at 2
		{
			repo:                     "test/valid",
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 0, "workflow_runs":[]}"`,
			want:                     2,
		},
		// 1 demanded, min at 2
		{
			repo:                     "test/valid",
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 0, "workflow_runs":[]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			want:                     2,
		},
		// 1 demanded, min at 1
		{
			repo:                     "test/valid",
			min:                      intPtr(1),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 0, "workflow_runs":[]}"`,
			want:                     1,
		},
		// 1 demanded, min at 1
		{
			repo:                     "test/valid",
			min:                      intPtr(1),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 0, "workflow_runs":[]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			want:                     1,
		},
		// fixed at 3
		{
			repo:                     "test/valid",
			min:                      intPtr(1),
			max:                      intPtr(3),
			fixed:                    intPtr(3),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 0, "workflow_runs":[]}"`,
			workflowRuns_in_progress: `{"total_count": 3, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}, {"status":"in_progress"}]}"`,
			want:                     3,
		},

		{
			description:              "Skipped job-level autoscaling with no explicit runner label (imply self-hosted, requested self-hosted+custom, 0 jobs from 3 workflows)",
			repo:                     "test/valid",
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 2,
		},

		{
			description:              "Skipped job-level autoscaling with no label (imply self-hosted, requested managed runners, 0 jobs from 3 workflows)",
			repo:                     "test/valid",
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued"}, {"status":"queued"}]}`,
				2: `{"jobs": [{"status": "in_progress"}, {"status":"completed"}]}`,
				3: `{"jobs": [{"status": "in_progress"}, {"status":"queued"}]}`,
			},
			want: 2,
		},

		{
			description:              "Skipped job-level autoscaling with default runner label (runners have self-hosted only, requested self-hosted+custom, 0 jobs from 3 workflows)",
			repo:                     "test/valid",
			labels:                   []string{"self-hosted"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 2,
		},

		{
			description:              "Skipped job-level autoscaling with custom runner label (runners have custom2, requested self-hosted+custom, 0 jobs from 5 workflows",
			repo:                     "test/valid",
			labels:                   []string{"custom2"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 2,
		},

		{
			description:              "Skipped job-level autoscaling with default runner label",
			repo:                     "test/valid",
			labels:                   []string{"self-hosted"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["managed-runner-label"]}, {"status":"queued", "labels":["managed-runner-label"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["managed-runner-label"]}, {"status":"completed", "labels":["managed-runner-label"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["managed-runner-label"]}, {"status":"queued", "labels":["managed-runner-label"]}]}`,
			},
			want: 2,
		},

		{
			description:              "Job-level autoscaling with default + custom runner label (5 requested from 3 workflows)",
			repo:                     "test/valid",
			labels:                   []string{"self-hosted", "custom"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 5,
		},

		{
			description:              "Job-level autoscaling with custom runner label (5 requested from 3 workflows)",
			repo:                     "test/valid",
			labels:                   []string{"custom"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 5,
		},
	}

	for i := range testcases {
		tc := testcases[i]

		log := zap.New(func(o *zap.Options) {
			o.Development = true
		})

		scheme := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(scheme)
		_ = v1alpha1.AddToScheme(scheme)

		testName := fmt.Sprintf("case %d", i)
		if tc.description != "" {
			testName = tc.description
		}

		t.Run(testName, func(t *testing.T) {
			server := fake.NewServer(
				fake.WithListRepositoryWorkflowRunsResponse(200, tc.workflowRuns, tc.workflowRuns_queued, tc.workflowRuns_in_progress),
				fake.WithListWorkflowJobsResponse(200, tc.workflowJobs),
				fake.WithListRunnersResponse(200, fake.RunnersListBody),
			)
			defer server.Close()
			client := newGithubClient(server)

			h := &HorizontalRunnerAutoscalerReconciler{
				Log:                   log,
				GitHubClient:          client,
				Scheme:                scheme,
				DefaultScaleDownDelay: DefaultScaleDownDelay,
			}

			rd := v1alpha1.RunnerDeployment{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: "testrd",
				},
				Spec: v1alpha1.RunnerDeploymentSpec{
					Template: v1alpha1.RunnerTemplate{
						Spec: v1alpha1.RunnerSpec{
							RunnerConfig: v1alpha1.RunnerConfig{
								Repository: tc.repo,
								Labels:     tc.labels,
							},
						},
					},
					Replicas: tc.fixed,
				},
				Status: v1alpha1.RunnerDeploymentStatus{
					DesiredReplicas: tc.sReplicas,
				},
			}

			hra := v1alpha1.HorizontalRunnerAutoscaler{
				Spec: v1alpha1.HorizontalRunnerAutoscalerSpec{
					MaxReplicas: tc.max,
					MinReplicas: tc.min,
					Metrics: []v1alpha1.MetricSpec{
						{
							Type: "TotalNumberOfQueuedAndInProgressWorkflowRuns",
						},
					},
				},
				Status: v1alpha1.HorizontalRunnerAutoscalerStatus{
					DesiredReplicas:            tc.sReplicas,
					LastSuccessfulScaleOutTime: tc.sTime,
				},
			}

			minReplicas, _, _, err := h.getMinReplicas(log, metav1Now.Time, hra)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			st := h.scaleTargetFromRD(context.Background(), rd)

			got, err := h.computeReplicasWithCache(log, metav1Now.Time, st, hra, minReplicas)
			if err != nil {
				if tc.err == "" {
					t.Fatalf("unexpected error: expected none, got %v", err)
				} else if err.Error() != tc.err {
					t.Fatalf("unexpected error: expected %v, got %v", tc.err, err)
				}
				return
			}

			if got != tc.want {
				t.Errorf("%d: incorrect desired replicas: want %d, got %d", i, tc.want, got)
			}
		})
	}
}

func TestDetermineDesiredReplicas_OrganizationalRunner(t *testing.T) {
	intPtr := func(v int) *int {
		return &v
	}

	metav1Now := metav1.Now()
	testcases := []struct {
		description string

		repos  []string
		org    string
		labels []string

		fixed     *int
		max       *int
		min       *int
		sReplicas *int
		sTime     *metav1.Time

		workflowRuns             string
		workflowRuns_queued      string
		workflowRuns_in_progress string

		workflowJobs map[int]string
		want         int
		err          string
	}{
		// 3 demanded, max at 3
		{
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}]}"`,
			want:                     3,
		},
		// 2 demanded, max at 3, currently 3, delay scaling down due to grace period
		{
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(2),
			max:                      intPtr(3),
			sReplicas:                intPtr(3),
			sTime:                    &metav1Now,
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			want:                     3,
		},
		// 3 demanded, max at 2
		{
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(2),
			max:                      intPtr(2),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}]}"`,
			want:                     2,
		},
		// 2 demanded, min at 2
		{
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 3, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			want:                     2,
		},
		// 1 demanded, min at 2
		{
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 0, "workflow_runs":[]}"`,
			want:                     2,
		},
		// 1 demanded, min at 2
		{
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(2),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 0, "workflow_runs":[]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			want:                     2,
		},
		// 1 demanded, min at 1
		{
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(1),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 0, "workflow_runs":[]}"`,
			want:                     1,
		},
		// 1 demanded, min at 1
		{
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(1),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 0, "workflow_runs":[]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			want:                     1,
		},
		// fixed at 3
		{
			org:                      "test",
			repos:                    []string{"valid"},
			fixed:                    intPtr(1),
			min:                      intPtr(1),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 0, "workflow_runs":[]}"`,
			workflowRuns_in_progress: `{"total_count": 3, "workflow_runs":[{"status":"in_progress"},{"status":"in_progress"},{"status":"in_progress"}]}"`,
			want:                     3,
		},
		// org runner, fixed at 3
		{
			org:                      "test",
			repos:                    []string{"valid"},
			fixed:                    intPtr(1),
			min:                      intPtr(1),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 0, "workflow_runs":[]}"`,
			workflowRuns_in_progress: `{"total_count": 3, "workflow_runs":[{"status":"in_progress"},{"status":"in_progress"},{"status":"in_progress"}]}"`,
			want:                     3,
		},
		// org runner, 1 demanded, min at 1, no repos
		{
			org:                      "test",
			min:                      intPtr(1),
			max:                      intPtr(3),
			workflowRuns:             `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 0, "workflow_runs":[]}"`,
			workflowRuns_in_progress: `{"total_count": 1, "workflow_runs":[{"status":"in_progress"}]}"`,
			err:                      "validating autoscaling metrics: spec.autoscaling.metrics[].repositoryNames is required and must have one more more entries for organizational runner deployment",
		},

		{
			description:              "Skipped job-level autoscaling (imply self-hosted, but jobs lack self-hosted labels, 0 requested from 3 workflows)",
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued"}, {"status":"queued"}]}`,
				2: `{"jobs": [{"status": "in_progress"}, {"status":"completed"}]}`,
				3: `{"jobs": [{"status": "in_progress"}, {"status":"queued"}]}`,
			},
			want: 2,
		},

		{
			description:              "Skipped job-level autoscaling (imply self-hosted, requested self-hosted+custom, 0 jobs from 3 workflows)",
			org:                      "test",
			repos:                    []string{"valid"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 2,
		},

		{
			description:              "Skipped job-level autoscaling (runners have self-hosted, requested self-hosted+custom, 0 jobs from 3 workflows)",
			org:                      "test",
			repos:                    []string{"valid"},
			labels:                   []string{"self-hosted"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 2,
		},

		{
			description:              "Job-level autoscaling (specified custom, 5 requested from 3 workflows)",
			org:                      "test",
			repos:                    []string{"valid"},
			labels:                   []string{"custom"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 5,
		},

		{
			description:              "Skipped job-level autoscaling (specified custom2, 0 requested from 3 workflows)",
			org:                      "test",
			repos:                    []string{"valid"},
			labels:                   []string{"custom2"},
			min:                      intPtr(2),
			max:                      intPtr(10),
			workflowRuns:             `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowRuns_queued:      `{"total_count": 1, "workflow_runs":[{"id": 1, "status":"queued"}]}"`,
			workflowRuns_in_progress: `{"total_count": 2, "workflow_runs":[{"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
				2: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"completed", "labels":["self-hosted", "custom"]}]}`,
				3: `{"jobs": [{"status": "in_progress", "labels":["self-hosted", "custom"]}, {"status":"queued", "labels":["self-hosted", "custom"]}]}`,
			},
			want: 2,
		},
	}

	for i := range testcases {
		tc := testcases[i]

		log := zap.New(func(o *zap.Options) {
			o.Development = true
		})

		scheme := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(scheme)
		_ = v1alpha1.AddToScheme(scheme)

		testName := fmt.Sprintf("case %d", i)
		if tc.description != "" {
			testName = tc.description
		}

		t.Run(testName, func(t *testing.T) {
			t.Helper()

			server := fake.NewServer(
				fake.WithListRepositoryWorkflowRunsResponse(200, tc.workflowRuns, tc.workflowRuns_queued, tc.workflowRuns_in_progress),
				fake.WithListWorkflowJobsResponse(200, tc.workflowJobs),
				fake.WithListRunnersResponse(200, fake.RunnersListBody),
			)
			defer server.Close()
			client := newGithubClient(server)

			h := &HorizontalRunnerAutoscalerReconciler{
				Log:                   log,
				Scheme:                scheme,
				GitHubClient:          client,
				DefaultScaleDownDelay: DefaultScaleDownDelay,
			}

			rd := v1alpha1.RunnerDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "testrd",
				},
				Spec: v1alpha1.RunnerDeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"foo": "bar",
						},
					},
					Template: v1alpha1.RunnerTemplate{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"foo": "bar",
							},
						},
						Spec: v1alpha1.RunnerSpec{
							RunnerConfig: v1alpha1.RunnerConfig{
								Organization: tc.org,
								Labels:       tc.labels,
							},
						},
					},
					Replicas: tc.fixed,
				},
				Status: v1alpha1.RunnerDeploymentStatus{
					DesiredReplicas: tc.sReplicas,
				},
			}

			hra := v1alpha1.HorizontalRunnerAutoscaler{
				Spec: v1alpha1.HorizontalRunnerAutoscalerSpec{
					ScaleTargetRef: v1alpha1.ScaleTargetRef{
						Name: "testrd",
					},
					MaxReplicas: tc.max,
					MinReplicas: tc.min,
					Metrics: []v1alpha1.MetricSpec{
						{
							Type:            v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns,
							RepositoryNames: tc.repos,
						},
					},
				},
				Status: v1alpha1.HorizontalRunnerAutoscalerStatus{
					DesiredReplicas:            tc.sReplicas,
					LastSuccessfulScaleOutTime: tc.sTime,
				},
			}

			minReplicas, _, _, err := h.getMinReplicas(log, metav1Now.Time, hra)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			st := h.scaleTargetFromRD(context.Background(), rd)

			got, err := h.computeReplicasWithCache(log, metav1Now.Time, st, hra, minReplicas)
			if err != nil {
				if tc.err == "" {
					t.Fatalf("unexpected error: expected none, got %v", err)
				} else if err.Error() != tc.err {
					t.Fatalf("unexpected error: expected %v, got %v", tc.err, err)
				}
				return
			}

			if got != tc.want {
				t.Errorf("%d: incorrect desired replicas: want %d, got %d", i, tc.want, got)
			}
		})
	}
}

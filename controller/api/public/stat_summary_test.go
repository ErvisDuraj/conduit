package public

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/prometheus/common/model"
	tap "github.com/runconduit/conduit/controller/gen/controller/tap"
	pb "github.com/runconduit/conduit/controller/gen/public"
	"github.com/runconduit/conduit/controller/k8s"
	pkgK8s "github.com/runconduit/conduit/pkg/k8s"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

type statSumExpected struct {
	err     error
	k8sRes  []string
	promRes model.Value
	req     pb.StatSummaryRequest
	res     pb.StatSummaryResponse
}

func testStatSummary(t *testing.T, expectations []statSumExpected) {
	for _, exp := range expectations {
		k8sObjs := []runtime.Object{}
		for _, res := range exp.k8sRes {
			decode := scheme.Codecs.UniversalDeserializer().Decode
			obj, _, err := decode([]byte(res), nil, nil)
			if err != nil {
				t.Fatalf("could not decode yml: %s", err)
			}
			k8sObjs = append(k8sObjs, obj)
		}

		clientSet := fake.NewSimpleClientset(k8sObjs...)
		lister := k8s.NewLister(clientSet)

		fakeGrpcServer := newGrpcServer(
			&MockProm{Res: exp.promRes},
			tap.NewTapClient(nil),
			lister,
			"conduit",
			[]string{},
		)
		err := lister.Sync()
		if err != nil {
			t.Fatalf("timed out wait for caches to sync")
		}

		rsp, err := fakeGrpcServer.StatSummary(context.TODO(), &exp.req)
		if err != exp.err {
			t.Fatalf("Expected error: %s, Got: %s", exp.err, err)
		}

		unsortedStatTables := rsp.GetOk().StatTables
		sort.Sort(byStatResult(unsortedStatTables))

		if !reflect.DeepEqual(exp.res.GetOk().StatTables, unsortedStatTables) {
			t.Fatalf("Expected: %+v, Got: %+v", &exp.res, rsp)
		}
	}
}

type byStatResult []*pb.StatTable

func (s byStatResult) Len() int {
	return len(s)
}

func (s byStatResult) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byStatResult) Less(i, j int) bool {
	if len(s[i].GetPodGroup().Rows) == 0 {
		return true
	}
	if len(s[j].GetPodGroup().Rows) == 0 {
		return false
	}

	return s[i].GetPodGroup().Rows[0].Resource.Type < s[j].GetPodGroup().Rows[0].Resource.Type
}

func TestStatSummary(t *testing.T) {
	t.Run("Successfully performs a query based on resource type", func(t *testing.T) {
		expectations := []statSumExpected{
			statSumExpected{
				err: nil,
				k8sRes: []string{`
apiVersion: apps/v1beta2
kind: Deployment
metadata:
  name: emoji
  namespace: emojivoto
spec:
  selector:
    matchLabels:
      app: emoji-svc
  strategy: {}
  template:
    spec:
      containers:
      - image: buoyantio/emojivoto-emoji-svc:v3
`, `
apiVersion: v1
kind: Pod
metadata:
  name: emojivoto-meshed
  namespace: emojivoto
  labels:
    app: emoji-svc
  annotations:
    conduit.io/proxy-version: testinjectversion
status:
  phase: Running
`, `
apiVersion: v1
kind: Pod
metadata:
  name: emojivoto-not-meshed
  namespace: emojivoto
  labels:
    app: emoji-svc
status:
  phase: Running
`, `
apiVersion: v1
kind: Pod
metadata:
  name: emojivoto-meshed-not-running
  namespace: emojivoto
  labels:
    app: emoji-svc
  annotations:
    conduit.io/proxy-version: testinjectversion
status:
  phase: Completed
`,
				},
				promRes: model.Vector{
					&model.Sample{
						Metric: model.Metric{
							"deployment":     "emoji",
							"namespace":      "emojivoto",
							"classification": "success",
						},
						Value:     123,
						Timestamp: 456,
					},
				},
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Namespace: "emojivoto",
							Type:      pkgK8s.Deployments,
						},
					},
					TimeWindow: "1m",
				},
				res: pb.StatSummaryResponse{
					Response: &pb.StatSummaryResponse_Ok_{ // https://github.com/golang/protobuf/issues/205
						Ok: &pb.StatSummaryResponse_Ok{
							StatTables: []*pb.StatTable{
								&pb.StatTable{
									Table: &pb.StatTable_PodGroup_{
										PodGroup: &pb.StatTable_PodGroup{
											Rows: []*pb.StatTable_PodGroup_Row{
												&pb.StatTable_PodGroup_Row{
													Resource: &pb.Resource{
														Namespace: "emojivoto",
														Type:      "deployments",
														Name:      "emoji",
													},
													Stats: &pb.BasicStats{
														SuccessCount: 123,
														FailureCount: 0,
														LatencyMsP50: 123,
														LatencyMsP95: 123,
														LatencyMsP99: 123,
													},
													TimeWindow:      "1m",
													MeshedPodCount:  1,
													RunningPodCount: 2,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		testStatSummary(t, expectations)
	})

	t.Run("Successfully queries for resource type 'all'", func(t *testing.T) {
		expectations := []statSumExpected{
			statSumExpected{
				err: nil,
				k8sRes: []string{`
apiVersion: apps/v1beta2
kind: Deployment
metadata:
  name: emoji-deploy
  namespace: emojivoto
spec:
  selector:
    matchLabels:
      app: emoji-svc
  strategy: {}
  template:
    spec:
      containers:
      - image: buoyantio/emojivoto-emoji-svc:v3
`, `
apiVersion: v1
kind: Service
metadata:
  name: emoji-svc
  namespace: emojivoto
spec:
  clusterIP: None
  selector:
    app: emoji-svc
`, `
apiVersion: v1
kind: Pod
metadata:
  name: emojivoto-pod-1
  namespace: not-right-emojivoto-namespace
  labels:
    app: emoji-svc
  annotations:
    conduit.io/proxy-version: testinjectversion
status:
  phase: Running
`, `
apiVersion: v1
kind: Pod
metadata:
  name: emojivoto-pod-2
  namespace: emojivoto
  labels:
    app: emoji-svc
  annotations:
    conduit.io/proxy-version: testinjectversion
status:
  phase: Running
`,
				},
				promRes: model.Vector{
					&model.Sample{
						Metric: model.Metric{
							"deployment":     "emoji-deploy",
							"namespace":      "emojivoto",
							"classification": "success",
						},
						Value:     123,
						Timestamp: 456,
					},
				},
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Namespace: "emojivoto",
							Type:      pkgK8s.All,
						},
					},
					TimeWindow: "1m",
				},
				res: pb.StatSummaryResponse{
					Response: &pb.StatSummaryResponse_Ok_{ // https://github.com/golang/protobuf/issues/205
						Ok: &pb.StatSummaryResponse_Ok{
							StatTables: []*pb.StatTable{
								&pb.StatTable{
									Table: &pb.StatTable_PodGroup_{
										PodGroup: &pb.StatTable_PodGroup{
											Rows: []*pb.StatTable_PodGroup_Row{},
										},
									},
								},
								&pb.StatTable{
									Table: &pb.StatTable_PodGroup_{
										PodGroup: &pb.StatTable_PodGroup{
											Rows: []*pb.StatTable_PodGroup_Row{
												&pb.StatTable_PodGroup_Row{
													Resource: &pb.Resource{
														Namespace: "emojivoto",
														Type:      "deployments",
														Name:      "emoji-deploy",
													},
													Stats: &pb.BasicStats{
														SuccessCount: 123,
														FailureCount: 0,
														LatencyMsP50: 123,
														LatencyMsP95: 123,
														LatencyMsP99: 123,
													},
													TimeWindow:      "1m",
													MeshedPodCount:  1,
													RunningPodCount: 1,
												},
											},
										},
									},
								},
								&pb.StatTable{
									Table: &pb.StatTable_PodGroup_{
										PodGroup: &pb.StatTable_PodGroup{
											Rows: []*pb.StatTable_PodGroup_Row{
												&pb.StatTable_PodGroup_Row{
													Resource: &pb.Resource{
														Namespace: "emojivoto",
														Type:      "pods",
														Name:      "emojivoto-pod-2",
													},
													TimeWindow:      "1m",
													MeshedPodCount:  1,
													RunningPodCount: 1,
												},
											},
										},
									},
								},
								&pb.StatTable{
									Table: &pb.StatTable_PodGroup_{
										PodGroup: &pb.StatTable_PodGroup{
											Rows: []*pb.StatTable_PodGroup_Row{
												&pb.StatTable_PodGroup_Row{
													Resource: &pb.Resource{
														Namespace: "emojivoto",
														Type:      "services",
														Name:      "emoji-svc",
													},
													TimeWindow:      "1m",
													MeshedPodCount:  1,
													RunningPodCount: 1,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		testStatSummary(t, expectations)
	})

	t.Run("Given an invalid resource type, returns error", func(t *testing.T) {
		expectations := []statSumExpected{
			statSumExpected{
				err: errors.New("rpc error: code = Unimplemented desc = unimplemented resource type: badtype"),
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Type: "badtype",
						},
					},
				},
			},
			statSumExpected{
				err: errors.New("rpc error: code = Unimplemented desc = unimplemented resource type: deployment"),
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Type: "deployment",
						},
					},
				},
			},
			statSumExpected{
				err: errors.New("rpc error: code = Unimplemented desc = unimplemented resource type: pod"),
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Type: "pod",
						},
					},
				},
			},
		}

		for _, exp := range expectations {
			clientSet := fake.NewSimpleClientset()
			lister := k8s.NewLister(clientSet)
			fakeGrpcServer := newGrpcServer(
				&MockProm{Res: exp.promRes},
				tap.NewTapClient(nil),
				lister,
				"conduit",
				[]string{},
			)

			_, err := fakeGrpcServer.StatSummary(context.TODO(), &exp.req)
			if err != nil || exp.err != nil {
				if (err == nil && exp.err != nil) ||
					(err != nil && exp.err == nil) ||
					(err.Error() != exp.err.Error()) {
					t.Fatalf("Unexpected error (Expected: %s, Got: %s)", exp.err, err)
				}
			}
		}
	})

	t.Run("Validates service stat requests", func(t *testing.T) {
		clientSet := fake.NewSimpleClientset()
		lister := k8s.NewLister(clientSet)
		fakeGrpcServer := newGrpcServer(
			&MockProm{Res: model.Vector{}},
			tap.NewTapClient(nil),
			lister,
			"conduit",
			[]string{},
		)

		invalidRequests := []statSumExpected{
			statSumExpected{
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Type: "services",
						},
					},
				},
			},
			statSumExpected{
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Type: "services",
						},
					},
					Outbound: &pb.StatSummaryRequest_ToResource{
						ToResource: &pb.Resource{
							Type: "pods",
						},
					},
				},
			},
			statSumExpected{
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Type: "pods",
						},
					},
					Outbound: &pb.StatSummaryRequest_FromResource{
						FromResource: &pb.Resource{
							Type: "services",
						},
					},
				},
			},
		}

		for _, invalid := range invalidRequests {
			rsp, err := fakeGrpcServer.StatSummary(context.TODO(), &invalid.req)

			if err != nil || rsp.GetError() == nil {
				t.Fatalf("Expected validation error on StatSummaryResponse, got %v, %v", rsp, err)
			}
		}

		validRequests := []statSumExpected{
			statSumExpected{
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Type: "pods",
						},
					},
					Outbound: &pb.StatSummaryRequest_ToResource{
						ToResource: &pb.Resource{
							Type: "services",
						},
					},
				},
			},
			statSumExpected{
				req: pb.StatSummaryRequest{
					Selector: &pb.ResourceSelection{
						Resource: &pb.Resource{
							Type: "services",
						},
					},
					Outbound: &pb.StatSummaryRequest_FromResource{
						FromResource: &pb.Resource{
							Type: "pods",
						},
					},
				},
			},
		}

		for _, valid := range validRequests {
			rsp, err := fakeGrpcServer.StatSummary(context.TODO(), &valid.req)

			if err != nil || rsp.GetError() != nil {
				t.Fatalf("Did not expect validation error on StatSummaryResponse, got %v, %v", rsp, err)
			}
		}
	})
}

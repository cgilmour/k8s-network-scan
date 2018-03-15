package main

import (
	// stdlib imports
	"fmt"
	"log"
	"os"
	// "os/signal"
	"strconv"
	"time"

	// k8s client-go imports
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	// corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

)

const (
	// Environment variables set by Downward API
	EnvVarPodName = "KNS_POD_NAME"
	EnvVarPodNamespace = "KNS_POD_NAMESPACE"
	EnvVarPodAddress = "KNS_POD_ADDRESS"

	ServiceLabelStartTime = "kube-network-scan/start-time"
)

func main() {
	// kubernetes api client
	cc, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("error in creating in-cluster config: %s", err)
	}
	client, err := kubernetes.NewForConfig(cc)
	if err != nil {
		log.Fatalf("error in creating in-cluster client: %s", err)
	}

	// get information about the network-scan service
	name := os.Getenv(EnvVarPodName)
	if name == "" {
		log.Fatalf("error getting pod information: no value for %s", EnvVarPodName)
	}
	namespace := os.Getenv(EnvVarPodNamespace)
	if namespace == "" {
		log.Fatalf("error getting pod information: no value for %s", EnvVarPodNamespace)
	}
	address := os.Getenv(EnvVarPodAddress)
	if address == "" {
		log.Fatalf("error gettting pod informatino: no value for %s", EnvVarPodAddress)
	}
	svc, err := client.CoreV1().Services(namespace).Get("network-scan", metav1.GetOptions{})
	if err != nil {
		log.Fatalf("error getting service information: %s", err)
	}

	startTime, ok := svc.ObjectMeta.Labels[ServiceLabelStartTime]
	if !ok {
		log.Fatalf("error getting label %s for service %s", ServiceLabelStartTime, svc.ObjectMeta.Name)
	}

	v, err := strconv.ParseInt(startTime, 10, 64)
	if err != nil {
		log.Fatalf("error converting value for label %s to timestamr: %s", ServiceLabelStartTime, err)
	}

	ts := time.Unix(v, 0)
	// make sure the time is reasonable
	now := time.Now()
	if ts.Before(now) {
		log.Fatal("scan start time is set in the past (ts=%s, now=%s)", ts, now)
	}
	if ts.After(now.Add(2 * time.Minute)) {
		log.Fatal("scan start time is set too far into the future (ts=%s, now=%s)", ts, now)
	}

	// set up http server with /healthz, /scan and /random/ handlers

	fmt.Println("Hello, playground")
}

func healthz() {
}



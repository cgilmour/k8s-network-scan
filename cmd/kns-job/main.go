package main

import (
	// stdlib imports
	"fmt"
	"log"
	"os"
	// "os/signal"
	"strings"
	"time"

	// k8s client-go imports
	corev1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/fields"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	// "k8s.io/client-go/tools/cache"

	//"golang.org/x/sys/unix"
)

const (
	// Environment variables set by Downward API
	EnvVarPodName = "KNS_POD_NAME"
	EnvVarPodNamespace = "KNS_POD_NAMESPACE"
	EnvVarPodAddress = "KNS_POD_ADDRESS"

	JobPodContainerName = "kns-job"

	ServiceLabelStartTime = "kube-network-scan/start-time"
	PodLabel = "kube-network-scan"
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

	// get information about the kns-job pod. we want the image name/tag
	jpn := os.Getenv(EnvVarPodName)
	if jpn == "" {
		log.Fatalf("error getting pod information: no value for %s", EnvVarPodName)
	}
	jpns := os.Getenv(EnvVarPodNamespace)
	if jpns == "" {
		log.Fatalf("error getting pod information: no value for %s", EnvVarPodNamespace)
	}
	jp, err := client.CoreV1().Pods(jpns).Get(jpn, metav1.GetOptions{})
	if err != nil {
		log.Fatalf("error getting pod information for pod %s:%s: %s", jpns, jpn, err)
	}
	log.Println("Loaded pod information:", jp.ObjectMeta.Name)

	// find the container image for the kns-job pod
	found := false
	img := ""
	for _, c := range jp.Spec.Containers {
		if c.Name == JobPodContainerName {
			found = true
			img = c.Image
			break
		}
	}
	if !found {
		log.Fatalf("error: no container named %s found in pod %s:%s", JobPodContainerName, jpns, jpn)
	}

	ns, err := createNamespace(client, fmt.Sprintf("%s-%s", "network-scan", getSuffixFromGeneratedName(jp.ObjectMeta.Name)))
	if err != nil {
		log.Fatalf("error creating namespace: %s", err)
	}
	log.Println("Created namespace", ns.ObjectMeta.Name)

	defer func(name string) {
		err := deleteNamespace(client, name)
		if err != nil {
			log.Fatalf("error deleting namespace %s: %s", name, err)
		}
		log.Println("Deleted namespace", name)
	}(ns.ObjectMeta.Name)

	r, err := createRole(client, ns.ObjectMeta.Name, "kns-pod")
	if err != nil {
		log.Fatalf("error creating role: %s", err)
	}

	sa, err := createServiceAccount(client, ns.ObjectMeta.Name, "kns-pod")
	if err != nil {
		log.Fatalf("error creating service account: %s", err)
	}

	rb, err := createRoleBinding(client, ns.ObjectMeta.Name, r.ObjectMeta.Name, sa.ObjectMeta.Name)
	if err != nil {
		log.Fatalf("error creating role binding: %s", err)
	}
	_ = rb // unused

	svc, err := createService(client, ns.ObjectMeta.Name, "network-scan")
	if err != nil {
		log.Fatalf("error creating service: %s", err)
	}
	log.Println("Created service", svc.ObjectMeta.Name)

	ds, err := createDaemonSet(client, ns.ObjectMeta.Name, "network-scan", replaceNameInImage(img, "kns-job", "kns-pod"), sa.ObjectMeta.Name)
	if err != nil {
		log.Fatalf("error creating daemonset: %s", err)
	}
	log.Println("Created daemonset", ds.ObjectMeta.Name)

	time.Sleep(1 * time.Minute)

	// run until killed or interrupted
	/*
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, unix.SIGTERM)
	select {
	case <-sigCh:
		close(stopCh)
	case <-doneCh:
		close(stopCh)
	}
	*/
}

func getSuffixFromGeneratedName(name string) string {
	tokens := strings.Split(name, "-")
	return tokens[len(tokens)-1]
}

func createNamespace(client kubernetes.Interface, name string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	return client.CoreV1().Namespaces().Create(ns)
}

func deleteNamespace(client kubernetes.Interface, name string) error {
	err := client.CoreV1().Namespaces().Delete(name, metav1.NewDeleteOptions(0))
	if err != nil {
		return err
	}
	err = wait.PollImmediate(1*time.Second, 1*time.Minute, func() (bool, error){
		nsList, err := client.CoreV1().Namespaces().List(
			metav1.ListOptions{
				FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String(),
			},
		)
		if err != nil {
			return false, err
		}
		if len(nsList.Items) > 0 {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	return nil
}

// createRole creates a role with permissions needed by kns-pod
func createRole(client kubernetes.Interface, ns string, role string) (*rbacv1.Role, error) {
	r := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: role,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{
					"",
				},
				Resources: []string{
					"services",
				},
				Verbs: []string{
					"get",
				},
			},
		},
	}
	return client.RbacV1().Roles(ns).Create(r)
}

// createServiceAccount creates a service account in the supplied namespace.
func createServiceAccount(client kubernetes.Interface, ns string, account string) (*corev1.ServiceAccount, error) {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: account,
		},
	}
	return client.CoreV1().ServiceAccounts(ns).Create(sa)
}

// createRoleBinding binds the role created to the service account.
func createRoleBinding(client kubernetes.Interface, ns string, role string, sa string) (*rbacv1.RoleBinding, error) {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: role,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind: "Role",
			Name: role,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind: "ServiceAccount",
				Name: sa,
			},
		},
	}
	return client.RbacV1().RoleBindings(ns).Create(rb)
}

func createService(client kubernetes.Interface, ns string, name string) (*corev1.Service, error) {
	startTS := time.Now().Add(1 * time.Minute).Unix()
	svc := &corev1.Service {
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				ServiceLabelStartTime: fmt.Sprintf("%d", startTS),
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				PodLabel: "",
			},
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Protocol: "TCP",
					Port: 80,
				},
			},
		},
	}
	return client.CoreV1().Services(ns).Create(svc)
}

func replaceNameInImage(img string, from string, to string) string {
	idx := strings.LastIndex(img, from)
	if idx < 0 {
		return img
	}
	return img[:idx] + to + img[idx+len(from):]
}

func createDaemonSet(client kubernetes.Interface, ns string, name string, image string, account string) (*appsv1.DaemonSet, error) {
	ds := &appsv1.DaemonSet {
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector {
				MatchLabels: map[string]string{
					"kube-network-scan": "",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					Labels: map[string]string{
						"kube-network-scan": "",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: name,
							Image: image,
							ImagePullPolicy: corev1.PullAlways,
							Env: []corev1.EnvVar{
								{
									Name: EnvVarPodName,
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
								{
									Name: EnvVarPodNamespace,
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
								{
									Name: EnvVarPodAddress,
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.podIP",
										},
									},
								},
							},
							ReadinessProbe: &corev1.Probe{
								Handler: corev1.Handler{
									HTTPGet:  &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(80),
									},
								},
								InitialDelaySeconds: 1,
								PeriodSeconds: 2,
							},
							LivenessProbe: &corev1.Probe{
								Handler: corev1.Handler{
									HTTPGet:  &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(80),
									},
								},
								InitialDelaySeconds: 3,
								PeriodSeconds: 10, 
							},

						},
					},
					ServiceAccountName: account,
					Tolerations: []corev1.Toleration{
						{Effect: corev1.TaintEffectNoExecute, Operator: corev1.TolerationOpExists},
					},
				},
			},
		},
	}
	return client.AppsV1().DaemonSets(ns).Create(ds)
}




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

package e2e

import (
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kyma-project/kim-snatch/test/utils"
)

// namespace where the project is deployed in
const namespace = "kyma-system"

// serviceAccountName created for the project
const serviceAccountName = "kim-snatch-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "kim-snatch-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "kim-snatch-metrics-binding"

// path to simple pod definition
const simplePod = "./test/e2e/resources/simple-pod.yaml"

const testNodeKymaLabelValue = "kim-snatch-test"

const testNodeName = "k3d-k3s-default-server-0"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// installing CRDs, and deploying the controller.
	BeforeAll(func() {
		By("label node")
		label := fmt.Sprintf("worker.gardener.cloud/pool=%s", testNodeKymaLabelValue)
		cmd := exec.Command("kubectl", "label", "node", testNodeName, label)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label node")

		By("creating manager namespace")
		cmd = exec.Command("kubectl", "create", "ns", namespace)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling kyma-system namespace")
		cmd = exec.Command("kubectl", "label", "namespace", namespace, "operator.kyma-project.io/managed-by=kyma")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "k3s-deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "k3s-undeploy")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)

		By("removing metrics cluster-role-binding")
		cmd = exec.Command("kubectl", "delete", "clusterrolebindings", metricsRoleBindingName)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should have priority-class", func() {
			// Get the name of the controller-manager pod
			pcName := "kim-snatch-priority-class"
			cmd := exec.Command("kubectl", "get",
				"priorityclass", pcName,
				"-n", namespace,
			)

			verifyControllerUp := func(g Gomega) {
				pcOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve kim-snatch-priority-class information")
				g.Expect(pcOutput).To(ContainSubstring(pcName))
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should have valid priority-class-name", func() {
			// Get the name of the controller-manager pod
			cmd := exec.Command("kubectl", "get", "pod",
				"-l app.kubernetes.io/component=kim-snatch",
				"-o", "go-template='{{ range .items }}{{ .spec.priorityClassName  }}{{ end }}'",
				"-n", namespace,
			)

			verifyPriorityClassName := func(g Gomega) {
				pcOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve priority-class-name information")
				g.Expect(pcOutput).To(ContainSubstring("kim-snatch-priority-class"))
			}
			Eventually(verifyPriorityClassName).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=snatch-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("validating that the ServiceMonitor for Prometheus is applied in the namespace")
			cmd = exec.Command("kubectl", "get", "ServiceMonitor", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "ServiceMonitor should exist")

			By("waiting for the metrics endpoint to be ready")
			verifyMetricsEndpointReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("8080"), "Metrics endpoint is not ready")
			}
			Eventually(verifyMetricsEndpointReady).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted).Should(Succeed())

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:7.78.0",
				"--", "/bin/sh", "-c", fmt.Sprintf(
					"curl -v http://%s.%s.svc.cluster.local:8080/metrics",
					metricsServiceName, namespace))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			metricsOutput := getMetricsOutput()
			Expect(metricsOutput).To(ContainSubstring(
				"go_gc_duration_seconds",
			))
		})

		It("should provisioned cert-manager", func() {
			By("validating that cert-manager has the certificate Secret")
			verifyCertManager := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secrets", "kim-snatch-certificates", "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyCertManager).Should(Succeed())
		})

		It("should have CA injection for mutating webhooks", func() {
			By("checking CA injection for mutating webhooks")
			verifyCAInjection := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"mutatingwebhookconfigurations.admissionregistration.k8s.io",
					"kim-snatch-mutating-webhook-configuration",
					"-o", "go-template={{ range .webhooks }}{{ .clientConfig.caBundle }}{{ end }}")
				mwhOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(mwhOutput)).To(BeNumerically(">", 10))
			}
			Eventually(verifyCAInjection).Should(Succeed())
		})

		It("should react to the CA bundle rotation", func() {
			By("deleting certificate")
			deleteCertificate := func(g Gomega) {
				cmd := exec.Command("kubectl", "delete",
					"certificates.cert-manager.io",
					"-n", namespace, "kim-snatch-kyma")
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(deleteCertificate).Should(Succeed())

			By("deleting certificate secret")
			deleteCertificateSecret := func(g Gomega) {
				cmd := exec.Command("kubectl", "delete",
					"secret", "-n", namespace,
					"kim-snatch-certificates")
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(deleteCertificateSecret).Should(Succeed())

			By("re-deploying certificate and issuer")
			deployIssuerAndCertificate := func(g Gomega) {
				cmd := exec.Command("make", "certmanager-deploy")
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to re-deploy the issuer and the certificate")
			}
			Eventually(deployIssuerAndCertificate).Should(Succeed())

			By("fetch new CA Bundle from certificate secret")
			var newCABundle string
			waitSecretCreated := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", "kim-snatch-certificates", "-n", namespace,
					"-o", `go-template={{index .data "ca.crt"}}`)

				var err error
				newCABundle, err = utils.Run(cmd)

				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(newCABundle).ShouldNot(BeEmpty())
			}
			Eventually(waitSecretCreated).Should(Succeed())

			By("checking CA injection for mutating webhooks")
			verifyCAInjection := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"mutatingwebhookconfigurations.admissionregistration.k8s.io",
					"kim-snatch-mutating-webhook-configuration",
					"-o", "go-template={{(index .webhooks 0).clientConfig.caBundle}}")
				mwhOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(mwhOutput).To(Equal(newCABundle))
			}
			Eventually(verifyCAInjection).WithPolling(5 * time.Second).Should(Succeed())
		})

		It("should trigger webhook", func() {
			By("create simple pod in labeled namespace")
			deleteCertificate := func(g Gomega) {
				cmd := exec.Command("kubectl", "apply", "-n", namespace, "-f", simplePod)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(deleteCertificate).Should(Succeed())

			By("verify webhook was triggered")
			verifyWebhookTriggered := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", "-n", namespace, "pause", "-o", "go-template={{.spec.affinity.nodeAffinity.preferredDuringSchedulingIgnoredDuringExecution}}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				expected := fmt.Sprintf("key:worker.gardener.cloud/pool operator:In values:[%s]", testNodeKymaLabelValue)
				g.Expect(output).To(ContainSubstring(expected))
			}
			Eventually(verifyWebhookTriggered).Should(Succeed())
		})

		It("should not trigger webhook", func() {
			By("create simple pod in non labeled namespace")
			deleteCertificate := func(g Gomega) {
				cmd := exec.Command("kubectl", "apply", "-f", simplePod)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(deleteCertificate).Should(Succeed())

			By("verify webhook was triggered")
			verifyWebhookTriggered := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", "pause", "-o", "go-template={{.spec.affinity.nodeAffinity.preferredDuringSchedulingIgnoredDuringExecution}}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("<no value>"))
			}
			Eventually(verifyWebhookTriggered).Should(Succeed())
		})
		// +kubebuilder:scaffold:e2e-webhooks-checks
	})
})

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
	Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
	return metricsOutput
}

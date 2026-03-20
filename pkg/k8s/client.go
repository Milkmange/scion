// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package k8s

import (
	"context"
	"fmt"
	"strings"

	"github.com/GoogleCloudPlatform/scion/pkg/k8s/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	SandboxGVR      = schema.GroupVersionResource{Group: "agents.x-k8s.io", Version: "v1alpha1", Resource: "sandboxes"}
	SandboxClaimGVR = schema.GroupVersionResource{Group: "extensions.agents.x-k8s.io", Version: "v1alpha1", Resource: "sandboxclaims"}

	// SecretProviderClassGVR is the GVR for the Secrets Store CSI Driver SecretProviderClass CRD.
	SecretProviderClassGVR = schema.GroupVersionResource{
		Group: "secrets-store.csi.x-k8s.io", Version: "v1", Resource: "secretproviderclasses",
	}
)

type Client struct {
	dynamic        dynamic.Interface
	Clientset      kubernetes.Interface
	Config         *rest.Config
	CurrentContext string
}

// NewClient creates a Kubernetes client using the default or specified kubeconfig.
// It uses the current context from the kubeconfig unless overridden via NewClientWithContext.
func NewClient(kubeconfigPath string) (*Client, error) {
	return NewClientWithContext(kubeconfigPath, "")
}

// NewClientWithContext creates a Kubernetes client targeting a specific context.
// If contextName is empty, the current context from the kubeconfig is used.
func NewClientWithContext(kubeconfigPath, contextName string) (*Client, error) {
	var config *rest.Config
	var err error
	var currentContext string

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	configLoader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		overrides,
	)

	config, err = configLoader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	rawConfig, err := configLoader.RawConfig()
	if err == nil {
		if contextName != "" {
			currentContext = contextName
		} else {
			currentContext = rawConfig.CurrentContext
		}
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	return &Client{
		dynamic:        dynClient,
		Clientset:      clientset,
		Config:         config,
		CurrentContext: currentContext,
	}, nil
}

// Verify performs a lightweight API call (ServerVersion) to validate that
// cluster connectivity and credentials work. This catches exec-based auth
// plugin failures (e.g. gke-gcloud-auth-plugin) early with actionable errors
// instead of letting them surface as confusing nested errors on the first real
// API call.
func (c *Client) Verify() error {
	_, err := c.Clientset.Discovery().ServerVersion()
	if err != nil {
		errMsg := err.Error()

		// Detect exec-based credential plugin failures and provide guidance.
		if strings.Contains(errMsg, "getting credentials: exec:") {
			hint := "Kubernetes credential plugin failed. "
			if strings.Contains(errMsg, "gke-gcloud-auth-plugin") {
				hint += "The gke-gcloud-auth-plugin could not obtain credentials. " +
					"Ensure the hub/broker process inherits the same environment as your shell " +
					"(HOME, PATH, CLOUDSDK_CONFIG, GOOGLE_APPLICATION_CREDENTIALS). " +
					"If running as a systemd service, verify these are set in the unit file. " +
					"You can test with: gke-gcloud-auth-plugin --version"
			} else {
				hint += "Ensure the credential plugin is installed and the process environment " +
					"includes the necessary variables (HOME, PATH, cloud SDK config)."
			}
			return fmt.Errorf("%s — underlying error: %w", hint, err)
		}

		return fmt.Errorf("failed to connect to Kubernetes cluster: %w", err)
	}

	return nil
}

func NewTestClient(dyn dynamic.Interface, cs kubernetes.Interface) *Client {
	return &Client{
		dynamic:   dyn,
		Clientset: cs,
	}
}

// Dynamic returns the dynamic Kubernetes client for CRD operations.
func (c *Client) Dynamic() dynamic.Interface { return c.dynamic }

func (c *Client) CreateSandboxClaim(ctx context.Context, namespace string, claim *v1alpha1.SandboxClaim) (*v1alpha1.SandboxClaim, error) {
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(claim)
	if err != nil {
		return nil, fmt.Errorf("failed to convert claim to unstructured: %w", err)
	}

	u := &unstructured.Unstructured{Object: unstructuredMap}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   v1alpha1.ExtensionsGroupVersion.Group,
		Version: v1alpha1.ExtensionsGroupVersion.Version,
		Kind:    "SandboxClaim",
	})

	result, err := c.dynamic.Resource(SandboxClaimGVR).Namespace(namespace).Create(ctx, u, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	var createdClaim v1alpha1.SandboxClaim
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.Object, &createdClaim); err != nil {
		return nil, fmt.Errorf("failed to convert result to claim: %w", err)
	}

	return &createdClaim, nil
}

func (c *Client) GetSandboxClaim(ctx context.Context, namespace, name string) (*v1alpha1.SandboxClaim, error) {
	result, err := c.dynamic.Resource(SandboxClaimGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var claim v1alpha1.SandboxClaim
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.Object, &claim); err != nil {
		return nil, fmt.Errorf("failed to convert result to claim: %w", err)
	}

	return &claim, nil
}

func (c *Client) ListSandboxClaims(ctx context.Context, namespace string, labelSelector string) (*v1alpha1.SandboxClaimList, error) {
	result, err := c.dynamic.Resource(SandboxClaimGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}

	var claimList v1alpha1.SandboxClaimList
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.Object, &claimList); err != nil {
		return nil, fmt.Errorf("failed to convert result to claim list: %w", err)
	}

	// Workaround for FromUnstructured not populating Items from dynamic client list
	if len(claimList.Items) == 0 && len(result.Items) > 0 {
		claimList.Items = make([]v1alpha1.SandboxClaim, len(result.Items))
		for i, item := range result.Items {
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, &claimList.Items[i]); err != nil {
				return nil, fmt.Errorf("failed to convert item %d: %w", i, err)
			}
		}
	}

	return &claimList, nil
}

func (c *Client) DeleteSandboxClaim(ctx context.Context, namespace, name string) error {
	return c.dynamic.Resource(SandboxClaimGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (c *Client) GetSandbox(ctx context.Context, namespace, name string) (*v1alpha1.Sandbox, error) {
	result, err := c.dynamic.Resource(SandboxGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var sandbox v1alpha1.Sandbox
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.Object, &sandbox); err != nil {
		return nil, fmt.Errorf("failed to convert result to sandbox: %w", err)
	}

	return &sandbox, nil
}

// CreateSecretProviderClass creates a SecretProviderClass CRD resource in the given namespace.
func (c *Client) CreateSecretProviderClass(ctx context.Context, namespace string, spc *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(SecretProviderClassGVR).Namespace(namespace).Create(ctx, spc, metav1.CreateOptions{})
}

// DeleteSecretProviderClass deletes a SecretProviderClass CRD resource by name.
func (c *Client) DeleteSecretProviderClass(ctx context.Context, namespace, name string) error {
	return c.dynamic.Resource(SecretProviderClassGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// ListSecretProviderClasses lists SecretProviderClass CRD resources matching a label selector.
func (c *Client) ListSecretProviderClasses(ctx context.Context, namespace, labelSelector string) (*unstructured.UnstructuredList, error) {
	return c.dynamic.Resource(SecretProviderClassGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
}

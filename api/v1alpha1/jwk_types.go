/*


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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// JWKSpec defines the desired state of JWK
type JWKSpec struct {
	KeyType       string `json:"keyType"`
	PublicKeyUse  string `json:"publicKeyUse,omitempty"`
	KeyOperations string `json:"keyOperations,omitempty"`

	ExpireDurationHours              int32 `json:"expireDurationHours,omitempty"`
	RotateDurationHoursBeforeExpired int32 `json:"rotateDurationHoursBeforeExpired,omitempty"`

	// Parameters for 'EC' KeyType
	CurveParameter string `json:"curveParameter,omitempty"`

	// Parameters for 'RSA' KeyType
	RSABitLength int32 `json:"rsaBitLength,omitempty"`
}

// JWKStatus defines the observed state of JWK
type JWKStatus struct {
	JWKs []Secret `json:"jwks,omitempty"`
}

// Secret defines the state of JWK Stored secret
type Secret struct {
	SecretRef corev1.ObjectReference `json:"secretRef,omitempty"`
	ExpireAt  string                 `json:"expireAt,omitempty"`
}

// +kubebuilder:object:root=true

// JWK is the Schema for the jwks API
type JWK struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   JWKSpec   `json:"spec,omitempty"`
	Status JWKStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// JWKList contains a list of JWK
type JWKList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []JWK `json:"items"`
}

func init() {
	SchemeBuilder.Register(&JWK{}, &JWKList{})
}

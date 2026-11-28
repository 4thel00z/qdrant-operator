package v1alpha1

// +kubebuilder:validation:Enum="True";"False";"Unknown"
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

type Condition struct {
	Type   string          `json:"type"`
	Status ConditionStatus `json:"status"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
}

type ClusterRef struct {
	Name string `json:"name"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

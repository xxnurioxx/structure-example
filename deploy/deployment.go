// Package deploy owns the Deployment domain: the shape users send us,
// the invariants we enforce, and how it maps onto Kubernetes.
//
// It is deliberately one package per feature (not models/, services/,
// repositories/). Everything you need to understand "deployments" lives here.
package deploy

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

const maxReplicas = 100

var (
	ErrNotFound = errors.New("deployment not found")
	ErrConflict = errors.New("deployment conflict")
	ErrInvalid  = errors.New("invalid deployment")
)

// Deployment is our public, opinionated view of a k8s Deployment.
// One container, one port. If you need more, you need Helm, not us.
type Deployment struct {
	Name      string  `json:"name"`
	Namespace string  `json:"namespace"`
	Spec      Spec    `json:"spec"`
	Status    *Status `json:"status,omitempty"`
}

type Spec struct {
	Image     string            `json:"image"`
	Replicas  *int32            `json:"replicas,omitempty"`
	Port      int32             `json:"port,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Resources *Resources        `json:"resources,omitempty"`
}

type Resources struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

type Status struct {
	Desired   int32     `json:"desired"`
	Ready     int32     `json:"ready"`
	Updated   int32     `json:"updated"`
	Available int32     `json:"available"`
	RolledOut bool      `json:"rolledOut"`
	CreatedAt time.Time `json:"createdAt"`
}

// Page / List are cursor pagination passed straight through to the API server.
type Page struct {
	Limit  int64
	Cursor string
}

type List struct {
	Items []Deployment `json:"items"`
	Next  string       `json:"next,omitempty"`
}

func (s Spec) replicas() int32 {
	if s.Replicas == nil {
		return 1
	}
	return *s.Replicas
}

// Validate enforces invariants up front so we return a precise 422
// instead of a wall of text from the API server.
func (d Deployment) Validate() error {
	v := &ValidationError{}

	v.addAll("namespace", validation.IsDNS1123Label(d.Namespace))
	v.addAll("name", validation.IsDNS1123Label(d.Name))

	if strings.TrimSpace(d.Spec.Image) == "" {
		v.add("spec.image", "is required")
	}
	if r := d.Spec.replicas(); r < 0 || r > maxReplicas {
		v.add("spec.replicas", fmt.Sprintf("must be between 0 and %d", maxReplicas))
	}
	if d.Spec.Port != 0 {
		v.addAll("spec.port", validation.IsValidPortNum(int(d.Spec.Port)))
	}
	for k := range d.Spec.Env {
		v.addAll("spec.env."+k, validation.IsEnvVarName(k))
	}
	for k, val := range d.Spec.Labels {
		if reservedLabel(k) {
			v.add("spec.labels."+k, "is reserved")
			continue
		}
		v.addAll("spec.labels."+k, validation.IsQualifiedName(k))
		v.addAll("spec.labels."+k, validation.IsValidLabelValue(val))
	}
	if r := d.Spec.Resources; r != nil {
		v.quantity("spec.resources.cpu", r.CPU)
		v.quantity("spec.resources.memory", r.Memory)
	}

	return v.orNil()
}

// ValidationError carries per-field messages. It matches ErrInvalid via errors.Is.
type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for f, msg := range e.Fields {
		parts = append(parts, f+": "+msg)
	}
	return "invalid deployment: " + strings.Join(parts, ", ")
}

func (e *ValidationError) Is(target error) bool { return target == ErrInvalid }

func (e *ValidationError) add(field, msg string) {
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	if prev, ok := e.Fields[field]; ok {
		msg = prev + "; " + msg
	}
	e.Fields[field] = msg
}

func (e *ValidationError) addAll(field string, msgs []string) {
	for _, m := range msgs {
		e.add(field, m)
	}
}

func (e *ValidationError) quantity(field, q string) {
	if q == "" {
		return
	}
	if _, err := resource.ParseQuantity(q); err != nil {
		e.add(field, err.Error())
	}
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

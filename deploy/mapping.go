package deploy

import (
	"maps"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/utils/ptr"
)

// toApply renders our Deployment as an SSA apply configuration.
// Only the fields we set are owned by us; everything else (HPA-managed
// replicas, injected sidecars, ...) is left alone by the API server.
func toApply(d Deployment) *appsv1ac.DeploymentApplyConfiguration {
	selector := map[string]string{labelName: d.Name}

	labels := maps.Clone(d.Spec.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[labelName] = d.Name
	labels[labelManaged] = fieldManager

	c := corev1ac.Container().
		WithName(d.Name).
		WithImage(d.Spec.Image).
		WithSecurityContext(corev1ac.SecurityContext().
			WithAllowPrivilegeEscalation(false).
			WithCapabilities(corev1ac.Capabilities().WithDrop("ALL")))

	// Sorted so the rendered object is deterministic (no spurious rollouts).
	for _, k := range slices.Sorted(maps.Keys(d.Spec.Env)) {
		c.WithEnv(corev1ac.EnvVar().WithName(k).WithValue(d.Spec.Env[k]))
	}

	if d.Spec.Port > 0 {
		c.WithPorts(corev1ac.ContainerPort().WithName("http").WithContainerPort(d.Spec.Port))
	}

	if r := d.Spec.Resources; r != nil {
		req, lim := corev1.ResourceList{}, corev1.ResourceList{}
		if r.CPU != "" {
			// Requests only: CPU limits cause throttling and buy nothing.
			req[corev1.ResourceCPU] = resource.MustParse(r.CPU) // validated upstream
		}
		if r.Memory != "" {
			q := resource.MustParse(r.Memory)
			req[corev1.ResourceMemory], lim[corev1.ResourceMemory] = q, q
		}
		c.WithResources(corev1ac.ResourceRequirements().WithRequests(req).WithLimits(lim))
	}

	return appsv1ac.Deployment(d.Name, d.Namespace).
		WithLabels(labels).
		WithSpec(appsv1ac.DeploymentSpec().
			WithReplicas(d.Spec.replicas()).
			WithSelector(metav1ac.LabelSelector().WithMatchLabels(selector)).
			WithTemplate(corev1ac.PodTemplateSpec().
				WithLabels(labels).
				WithSpec(corev1ac.PodSpec().WithContainers(c))))
}

func fromK8s(o *appsv1.Deployment) Deployment {
	d := Deployment{
		Name:      o.Name,
		Namespace: o.Namespace,
		Spec: Spec{
			Replicas: o.Spec.Replicas,
			Labels:   userLabels(o.Labels),
		},
	}

	if cs := o.Spec.Template.Spec.Containers; len(cs) > 0 {
		c := cs[0]
		d.Spec.Image = c.Image
		if len(c.Ports) > 0 {
			d.Spec.Port = c.Ports[0].ContainerPort
		}
		if len(c.Env) > 0 {
			d.Spec.Env = make(map[string]string, len(c.Env))
			for _, e := range c.Env {
				d.Spec.Env[e.Name] = e.Value
			}
		}
		cpu, mem := c.Resources.Requests[corev1.ResourceCPU], c.Resources.Requests[corev1.ResourceMemory]
		if !cpu.IsZero() || !mem.IsZero() {
			d.Spec.Resources = &Resources{CPU: quantity(cpu), Memory: quantity(mem)}
		}
	}

	desired := ptr.Deref(o.Spec.Replicas, 1)
	st := o.Status
	d.Status = &Status{
		Desired:   desired,
		Ready:     st.ReadyReplicas,
		Updated:   st.UpdatedReplicas,
		Available: st.AvailableReplicas,
		RolledOut: st.ObservedGeneration >= o.Generation &&
			st.UpdatedReplicas == desired &&
			st.AvailableReplicas == desired,
		CreatedAt: o.CreationTimestamp.Time,
	}
	return d
}

func userLabels(in map[string]string) map[string]string {
	out := maps.Clone(in)
	maps.DeleteFunc(out, func(k, _ string) bool { return reservedLabel(k) })
	if len(out) == 0 {
		return nil
	}
	return out
}

func reservedLabel(k string) bool { return k == labelName || k == labelManaged }

func quantity(q resource.Quantity) string {
	if q.IsZero() {
		return ""
	}
	return q.String()
}

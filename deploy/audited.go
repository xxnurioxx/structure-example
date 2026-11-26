package deploy

import (
	"context"
	"maps"
	"slices"

	"github.com/acme/kdeploy/audit"
)

// Audited wraps *Kube and records every Deployment operation and its outcome.
// Kube stays pure Kubernetes; auditing is composed on top in main.
//
// It records what the user did ("apply default/web"), not each raw API call:
// the Kubernetes API server already has its own audit log for that.
type Audited struct {
	next *Kube
	rec  audit.Recorder
}

func NewAudited(next *Kube, rec audit.Recorder) *Audited {
	return &Audited{next: next, rec: rec}
}

func (a *Audited) Apply(ctx context.Context, d Deployment) (Deployment, error) {
	out, err := a.next.Apply(ctx, d)
	a.record(ctx, "deployment.apply", d.Namespace, d.Name, err, map[string]any{
		"image":    d.Spec.Image,
		"replicas": d.Spec.replicas(),
		// Keys only: env values are where people put secrets.
		"env_keys": slices.Sorted(maps.Keys(d.Spec.Env)),
	})
	return out, err
}

func (a *Audited) Get(ctx context.Context, ns, name string) (Deployment, error) {
	out, err := a.next.Get(ctx, ns, name)
	a.record(ctx, "deployment.get", ns, name, err, nil)
	return out, err
}

func (a *Audited) List(ctx context.Context, ns string, p Page) (List, error) {
	out, err := a.next.List(ctx, ns, p)
	a.record(ctx, "deployment.list", ns, "", err, map[string]any{"count": len(out.Items)})
	return out, err
}

func (a *Audited) Delete(ctx context.Context, ns, name string) error {
	err := a.next.Delete(ctx, ns, name)
	a.record(ctx, "deployment.delete", ns, name, err, nil)
	return err
}

func (a *Audited) record(ctx context.Context, action, ns, name string, err error, details map[string]any) {
	target := ns
	if name != "" {
		target = ns + "/" + name
	}
	e := audit.New(ctx, action, target).Result(err)
	e.Details = details
	a.rec.Record(ctx, e)
}

package deploy

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	fieldManager = "kdeploy"
	labelName    = "app.kubernetes.io/name"
	labelManaged = "app.kubernetes.io/managed-by"
)

// Kube is the Kubernetes-backed implementation. It is a concrete type:
// consumers declare the interface they need (see httpapi.Deployer).
type Kube struct {
	client kubernetes.Interface
}

func NewKube(client kubernetes.Interface) *Kube {
	return &Kube{client: client}
}

// Apply creates or updates a Deployment using Server-Side Apply.
// SSA makes this idempotent and declarative: no read-modify-write loops,
// no resourceVersion juggling, and field ownership is tracked by the API server.
func (k *Kube) Apply(ctx context.Context, d Deployment) (Deployment, error) {
	if err := d.Validate(); err != nil {
		return Deployment{}, err
	}

	// Never hijack a Deployment someone else owns.
	switch _, err := k.get(ctx, d.Namespace, d.Name); {
	case err == nil, isNotFound(err):
	default:
		return Deployment{}, err
	}

	out, err := k.client.AppsV1().Deployments(d.Namespace).Apply(ctx, toApply(d), metav1.ApplyOptions{
		FieldManager: fieldManager,
		Force:        true,
	})
	if err != nil {
		return Deployment{}, mapErr(err)
	}
	return fromK8s(out), nil
}

func (k *Kube) Get(ctx context.Context, ns, name string) (Deployment, error) {
	obj, err := k.get(ctx, ns, name)
	if err != nil {
		return Deployment{}, err
	}
	return fromK8s(obj), nil
}

func (k *Kube) List(ctx context.Context, ns string, p Page) (List, error) {
	res, err := k.client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labelManaged + "=" + fieldManager,
		Limit:         p.Limit,
		Continue:      p.Cursor,
	})
	if err != nil {
		return List{}, mapErr(err)
	}

	out := List{Items: make([]Deployment, 0, len(res.Items)), Next: res.Continue}
	for i := range res.Items {
		out.Items = append(out.Items, fromK8s(&res.Items[i]))
	}
	return out, nil
}

func (k *Kube) Delete(ctx context.Context, ns, name string) error {
	obj, err := k.get(ctx, ns, name)
	if err != nil {
		return err
	}
	// UID precondition: if it was replaced between Get and Delete, don't touch it.
	policy := metav1.DeletePropagationBackground
	return mapErr(k.client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{
		Preconditions:     &metav1.Preconditions{UID: &obj.UID},
		PropagationPolicy: &policy,
	}))
}

// Ping is a readiness probe against the API server.
func (k *Kube) Ping(ctx context.Context) error {
	return k.client.Discovery().RESTClient().Get().AbsPath("/readyz").Do(ctx).Error()
}

// get fetches a Deployment and refuses anything we don't manage:
// this API is a scoped tool, not a kubectl proxy.
func (k *Kube) get(ctx context.Context, ns, name string) (*appsv1.Deployment, error) {
	obj, err := k.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, mapErr(err)
	}
	if obj.Labels[labelManaged] != fieldManager {
		return nil, fmt.Errorf("%w: %s/%s is not managed by %s", ErrConflict, ns, name, fieldManager)
	}
	return obj, nil
}

func isNotFound(err error) bool { return err != nil && errors.Is(err, ErrNotFound) }

// mapErr translates API server errors into domain errors so callers
// never import k8s.io/apimachinery just to check for a 404.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case apierrors.IsConflict(err), apierrors.IsAlreadyExists(err):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	case apierrors.IsInvalid(err):
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	default:
		return fmt.Errorf("kubernetes: %w", err)
	}
}

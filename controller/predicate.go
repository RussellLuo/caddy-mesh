package controller

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func IgnoreNamespaces(namespaces ...string) predicate.Funcs {
	ignored := NewSet(namespaces...)
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		return !ignored.Contains(object.GetNamespace())
	})
}

func IgnoreService(namespace, name string) predicate.Funcs {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		if d, ok := object.(*corev1.Service); ok {
			return d.GetNamespace() != namespace || d.GetName() != name
		}
		return true
	})
}

func IgnoreLabel(key, value string) predicate.Funcs {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		labels := object.GetLabels()
		if v, ok := labels[key]; ok && v == value {
			return false
		}
		return true
	})
}

type Set struct {
	m map[string]struct{}
}

func NewSet(elems ...string) *Set {
	m := make(map[string]struct{})
	for _, elem := range elems {
		m[elem] = struct{}{}
	}
	return &Set{m: m}
}

func (s *Set) Contains(elem string) bool {
	_, ok := s.m[elem]
	return ok
}

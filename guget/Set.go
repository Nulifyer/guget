package main

type Set[T comparable] map[T]struct{}

func NewSet[T comparable]() Set[T] { return make(Set[T]) }
func (s Set[T]) Add(v T)           { s[v] = struct{}{} }
func (s Set[T]) Remove(v T)        { delete(s, v) }
func (s Set[T]) Contains(v T) bool { _, ok := s[v]; return ok }
func (s Set[T]) Len() int          { return len(s) }

func (s Set[T]) ToSlice() []T {
	result := make([]T, 0, len(s))
	for v := range s {
		result = append(result, v)
	}
	return result
}

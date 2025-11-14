package telecaster_test

func with[T any](value *T, fn func(*T)) *T {
	v := *value
	fn(&v)
	return &v
}

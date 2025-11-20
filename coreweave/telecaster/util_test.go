package telecaster_test

import "fmt"

const (
	AcceptanceTestPrefix = "tf-acc-tc-"
)

func with[T any](value *T, fn func(*T)) *T {
	v := *value
	fn(&v)
	return &v
}

// slugify creates a test resource slug with the acceptance test prefix and random suffix
func slugify(name string, randomInt int) string {
	return fmt.Sprintf("%s%s-%d", AcceptanceTestPrefix, name, randomInt)
}

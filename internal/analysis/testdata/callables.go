package conformance

func Outer(flag bool) int {
	handler := func(value int) int {
		if value > 0 && flag {
			return value
		}
		return 0
	}
	if flag {
		return handler(1)
	}
	return 0
}

var PackageHandler = func(values []int) int {
	for range values {
	}
	return len(values)
}

func Register() {
	Use(func(value int) bool {
		return value > 0 || value < -10
	})
}

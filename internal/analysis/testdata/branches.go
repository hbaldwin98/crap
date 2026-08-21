package conformance

func Count(value any, ready bool, ch chan int) int {
	if ready {
		return 1
	}
	for ready {
		break
	}
	switch value {
	case 1, 2:
	case 3:
	default:
	}
	switch value.(type) {
	case int:
	default:
	}
	select {
	case <-ch:
	default:
	}
	if ready && value != nil || ch != nil {
		return 2
	}
	return 0
}

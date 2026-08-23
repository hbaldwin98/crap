package library

type Greeter interface {
	Greet(name string) string
}

type Formal struct{}

func (formal Formal) Greet(name string) string {
	return prefix() + name
}

type Casual struct{}

func (casual *Casual) Greet(name string) string {
	return "hey " + name
}

func prefix() string {
	return "hello "
}

func GreetWith(greeter Greeter, name string) string {
	return greeter.Greet(name)
}

func FormalGreeting(name string) string {
	return Formal{}.Greet(name)
}

func total(values []int) int {
	sum := 0
	each := func(value int) {
		sum += value
	}
	for _, value := range values {
		each(value)
	}
	return len(values) + sum
}

func doubles(values []int) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		result = append(result, value*2)
	}
	return result
}

package library

import "strings"

func shout(name string) string {
	return strings.ToUpper(GreetWith(&Casual{}, name))
}

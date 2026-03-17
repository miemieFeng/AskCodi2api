package util

import (
	"math/rand"
)

const (
	upperChars   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lowerChars   = "abcdefghijklmnopqrstuvwxyz"
	digitChars   = "0123456789"
	specialChars = "!@#$%^&*"
	allChars     = upperChars + lowerChars + digitChars + specialChars
)

func GeneratePassword(length int) string {
	if length < 4 {
		length = 12
	}
	pwd := make([]byte, length)
	pwd[0] = upperChars[rand.Intn(len(upperChars))]
	pwd[1] = lowerChars[rand.Intn(len(lowerChars))]
	pwd[2] = digitChars[rand.Intn(len(digitChars))]
	pwd[3] = specialChars[rand.Intn(len(specialChars))]
	for i := 4; i < length; i++ {
		pwd[i] = allChars[rand.Intn(len(allChars))]
	}
	// Shuffle
	rand.Shuffle(length, func(i, j int) {
		pwd[i], pwd[j] = pwd[j], pwd[i]
	})
	return string(pwd)
}

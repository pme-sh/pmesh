package security

import "crypto/sha1"

func GenerateKeyRaw(secret string, salt []byte, n int) []byte {
	shaSteps := (n + 19) / 20
	buffer := make([]byte, shaSteps*20)
	sha := sha1.New()
	sha.Write([]byte(secret))
	sha.Write(salt)
	for i := 0; i < shaSteps; i++ {
		sum := sha.Sum(nil)
		copy(buffer[i*20:], sum)
		sha.Write(salt)
	}
	return buffer[:n]
}
func GenerateKey(secret string, salt string, n int) []byte {
	return GenerateKeyRaw(secret, []byte(salt), n)
}

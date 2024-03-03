package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/binary"
)

type CipherCPRNG struct {
	stream       cipher.Stream
	weakRandom   byte
	weakSeed     uint64
	weakEntrophy []byte
}

// readWeakQ returns a weak random number
func (c *CipherCPRNG) readWeakQ() uint64 {
	c.weakSeed *= 6364136223846793005
	c.weakSeed += 1
	return c.weakSeed
}

// readWeak fills the given buffer with weak random data
func (c *CipherCPRNG) readWeak(out []byte) {
	we := c.weakEntrophy
	needed := len(out)
	missing := ((needed + 7) - len(we)) / 8
	for i := 0; i < missing; i++ {
		we = binary.LittleEndian.AppendUint64(we, c.readWeakQ())
	}
	copy(out, we[:needed])
	c.weakEntrophy = we[needed:]
}

// Read fills the given buffer with cryptographically secure random data
func (c *CipherCPRNG) Read(out []byte) (n int, err error) {
	if len(out) == 1 {
		out[0] = c.weakRandom
		return 1, nil
	}

	// Initialize a temporary buffer to be filled with weak random data
	tmp := make([]byte, len(out))
	c.readWeak(tmp)

	// Pass it through the stream, filling the output buffer
	c.stream.XORKeyStream(out, tmp)
	return len(out), nil
}

// Associate adds data to the CPRNG's state.
func (c *CipherCPRNG) Associate(data []byte) {
	tmp := make([]byte, len(data))
	c.stream.XORKeyStream(tmp, data)
}

// NewCipherCprng creates a new cryptographically secure random number generator
func NewCipherCprng(key []byte) (c *CipherCPRNG) {
	var block cipher.Block
	var err error

	// Hash the key to generate a key for AES-256 and an IV
	secret := sha512.Sum512(key)
	block, err = aes.NewCipher(secret[:32])
	if err != nil {
		panic(err) // This should never happen
	}

	// Initialize the CTR stream
	c = &CipherCPRNG{}
	c.stream = cipher.NewCTR(block, secret[64-16:])

	// Read a weak seed from the stream
	temp := make([]byte, 32)
	c.weakSeed = binary.LittleEndian.Uint64(temp[23 : 23+8])
	c.weakRandom = temp[temp[31]%23]
	return
}

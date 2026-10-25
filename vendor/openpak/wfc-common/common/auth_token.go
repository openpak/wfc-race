package common

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func generateRandom(n int) []byte {
	key := make([]byte, n)

	read, err := rand.Read(key)
	if err != nil {
		panic(err)
	}

	if read != n {
		panic("short rand.Read()")
	}

	return key
}

var (
	authTokenKey   = generateRandom(16)
	authTokenIV    = generateRandom(16)
	authTokenMagic = generateRandom(8)

	loginTicketKey   = generateRandom(16)
	loginTicketIV    = generateRandom(16)
	loginTicketMagic = generateRandom(4)
)

var (
	ErrTokenMagic   = errors.New("invalid auth token or login ticket magic")
	ErrTokenExpired = errors.New("auth token or login ticket expired")
	ErrTokenLength  = errors.New("invalid auth token or login ticket length")
)

// LoadTokenKey preserves the native ticket format while making keys stable across
// independently deployed processes. The old process-random scheme invalidated all
// tickets on every backend restart. Load once, before starting any listener.
func LoadTokenKey() error {
	path := os.Getenv("WFC_TOKEN_KEY_FILE")
	if path == "" {
		return fmt.Errorf("WFC_TOKEN_KEY_FILE is required")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(key) < 32 {
		return fmt.Errorf("WFC token key must contain at least 32 bytes")
	}
	derive := func(label string, n int) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte("openpak-wfc-v1/" + label))
		return m.Sum(nil)[:n]
	}
	authTokenKey = derive("auth-key", 16)
	authTokenIV = derive("auth-iv", 16)
	authTokenMagic = derive("auth-magic", 8)
	loginTicketKey = derive("login-key", 16)
	loginTicketIV = derive("login-iv", 16)
	loginTicketMagic = derive("login-magic", 4)
	return nil
}

type NASAuthToken struct {
	IssueTime         uint64
	UserID            uint64
	ConsoleFriendCode uint64
	Region            byte
	Lang              byte
	UnitCode          byte
	GameCode          [4]byte
	GsbrCode          [16]byte
	Challenge         [8]byte
	InGameScreenName  [64]byte
	Magic             [8]byte
}

var nasAuthTokenSize = (binary.Size(NASAuthToken{}) + aes.BlockSize - 1) & ^(aes.BlockSize - 1)

func (t NASAuthToken) Marshal() string {
	t.IssueTime = uint64(time.Now().UTC().Unix())
	copy(t.Magic[:], authTokenMagic)

	var buf bytes.Buffer
	ShouldNotError(binary.Write(&buf, binary.LittleEndian, t))

	// Pad to CBC block size
	data := append(buf.Bytes(), make([]byte, nasAuthTokenSize-len(buf.Bytes()))...)

	block, err := aes.NewCipher(authTokenKey)
	ShouldNotError(err)
	cipher.NewCBCEncrypter(block, authTokenIV).CryptBlocks(data, data)

	return "NDS" + Base64DwcEncoding.EncodeToString(data)
}

func (t *NASAuthToken) Unmarshal(data string) error {
	if !strings.HasPrefix(data, "NDS") {
		return ErrTokenLength
	}

	blob, err := Base64DwcEncoding.DecodeString(data[3:])
	if err != nil {
		return err
	}

	if len(blob) != nasAuthTokenSize {
		return ErrTokenLength
	}

	block, err := aes.NewCipher(authTokenKey)
	ShouldNotError(err)
	cipher.NewCBCDecrypter(block, authTokenIV).CryptBlocks(blob, blob)

	reader := bytes.NewReader(blob)
	if err := binary.Read(reader, binary.LittleEndian, t); err != nil {
		return err
	}

	if !bytes.Equal(t.Magic[:], authTokenMagic) {
		return ErrTokenMagic
	}

	currentTime := time.Now().UTC()
	issueTime := time.Unix(int64(t.IssueTime), 0)
	if issueTime.Before(currentTime.Add(-10*time.Minute)) || issueTime.After(currentTime) {
		return ErrTokenExpired
	}

	return nil
}

type GPCMLoginTicket struct {
	IssueTime uint64
	ProfileID uint32
	Magic     [4]byte
}

var gpcmLoginTicketSize = (binary.Size(GPCMLoginTicket{}) + aes.BlockSize - 1) & ^(aes.BlockSize - 1)

func (t GPCMLoginTicket) Marshal() string {
	t.IssueTime = uint64(time.Now().UTC().Unix())
	copy(t.Magic[:], loginTicketMagic)

	var buf bytes.Buffer
	ShouldNotError(binary.Write(&buf, binary.LittleEndian, t))

	// Pad to CBC block size
	data := append(buf.Bytes(), make([]byte, gpcmLoginTicketSize-len(buf.Bytes()))...)

	block, err := aes.NewCipher(loginTicketKey)
	ShouldNotError(err)
	cipher.NewCBCEncrypter(block, loginTicketIV).CryptBlocks(data, data)

	return base64.StdEncoding.EncodeToString(data)
}

func (t *GPCMLoginTicket) Unmarshal(ticket string) error {
	blob, err := base64.StdEncoding.DecodeString(ticket)
	if err != nil {
		return err
	}

	if len(blob) != gpcmLoginTicketSize {
		return ErrTokenLength
	}

	block, err := aes.NewCipher(loginTicketKey)
	ShouldNotError(err)
	cipher.NewCBCDecrypter(block, loginTicketIV).CryptBlocks(blob, blob)

	reader := bytes.NewReader(blob)
	if err := binary.Read(reader, binary.LittleEndian, t); err != nil {
		return err
	}

	if !bytes.Equal(t.Magic[:], loginTicketMagic) {
		return ErrTokenMagic
	}

	currentTime := time.Now().UTC()
	issueTime := time.Unix(int64(t.IssueTime), 0)
	if issueTime.Before(currentTime.Add(-48*time.Hour)) || issueTime.After(currentTime) {
		return ErrTokenExpired
	}

	return nil
}

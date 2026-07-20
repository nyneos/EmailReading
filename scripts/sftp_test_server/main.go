// Lightweight local SFTP server for testing transform destination_type=SFTP.
// No Docker — run: go run ./scripts/sftp_test_server/
//
// UI / rule form:
//   Host 127.0.0.1  Port 2222  User cimplr  Password cimplr123  Folder upload
// Files appear under ./sftp-data/upload/ when remote folder is "upload"
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	defaultPort = "2222"
	username    = "cimplr"
	password    = "cimplr123"
	uploadRoot  = "./sftp-data"
	hostKeyPEM  = "./sftp-data/dev_host_key"
)

func listenAddr() string {
	port := strings.TrimSpace(os.Getenv("SFTP_TEST_PORT"))
	if port == "" {
		port = defaultPort
	}
	return "127.0.0.1:" + port
}

func main() {
	if err := os.MkdirAll(uploadRoot, 0o755); err != nil {
		log.Fatalf("mkdir upload root: %v", err)
	}
	signer, err := loadOrCreateHostKey(hostKeyPEM)
	if err != nil {
		log.Fatalf("host key: %v", err)
	}

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == username && string(pass) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("invalid credentials")
		},
	}
	config.AddHostKey(signer)

	addr := listenAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	abs, _ := filepath.Abs(uploadRoot)
	log.Printf("SFTP test server listening on %s", addr)
	log.Printf("  user=%s password=%s remote folder=upload", username, password)
	log.Printf("  files saved under %s/upload/", abs)
	log.Printf("  (set SFTP_TEST_PORT to use another port if %s is busy)", strings.Split(addr, ":")[1])
	log.Printf("Stop with Ctrl+C")

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn, config)
	}
}

func handleConn(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		log.Printf("ssh handshake: %v", err)
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			log.Printf("channel accept: %v", err)
			continue
		}
		go handleSession(channel, requests)
	}
}

func handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for req := range requests {
		switch req.Type {
		case "subsystem":
			if len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
				_ = req.Reply(true, nil)
				serveSFTP(channel)
				return
			}
		}
		_ = req.Reply(false, nil)
	}
}

func serveSFTP(rw io.ReadWriteCloser) {
	defer rw.Close()
	h := rootHandler{}
	handlers := sftp.Handlers{
		FileGet:  h,
		FilePut:  h,
		FileCmd:  h,
		FileList: h,
	}
	server := sftp.NewRequestServer(rw, handlers)
	if err := server.Serve(); err != nil && err != io.EOF {
		log.Printf("sftp serve: %v", err)
	}
}

type rootHandler struct{}

func (rootHandler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	path, err := safePath(r.Filepath)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (rootHandler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	path, err := safePath(r.Filepath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}

func (rootHandler) Filecmd(r *sftp.Request) error {
	if r.Method == "Mkdir" {
		path, err := safePath(r.Filepath)
		if err != nil {
			return err
		}
		return os.MkdirAll(path, 0o755)
	}
	return nil
}

func (rootHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	path, err := safePath(r.Filepath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return listerAt(infos), nil
}

func safePath(name string) (string, error) {
	name = strings.TrimPrefix(filepath.ToSlash(name), "/")
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid path")
	}
	return filepath.Join(uploadRoot, clean), nil
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(f []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(f, l[offset:])
	if int(offset)+n >= len(l) {
		return n, io.EOF
	}
	return n, nil
}

func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(key)
}

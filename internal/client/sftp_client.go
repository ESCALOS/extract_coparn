package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"time"

	"extract_coparn/internal/config"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SFTPClient struct {
	cfg config.SFTPConfig
}

func NewSFTPClient(cfg config.SFTPConfig) *SFTPClient {
	return &SFTPClient{cfg: cfg}
}

func (c *SFTPClient) UploadFile(ctx context.Context, localPath, fileName string) (string, error) {
	sshClient, sftpClient, err := c.connect(ctx)
	if err != nil {
		return "", err
	}
	defer sshClient.Close()
	defer sftpClient.Close()

	remotePath := path.Join(c.cfg.RemoteDir, fileName)
	if err := sftpClient.MkdirAll(path.Dir(remotePath)); err != nil {
		return "", err
	}

	src, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := sftpClient.Create(remotePath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	return remotePath, nil
}

func (c *SFTPClient) HealthCheck(ctx context.Context) error {
	sshClient, sftpClient, err := c.connect(ctx)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	defer sftpClient.Close()
	return nil
}

func (c *SFTPClient) connect(ctx context.Context) (*ssh.Client, *sftp.Client, error) {
	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	dialer := &net.Dialer{Timeout: c.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}

	sshCfg := &ssh.ClientConfig{
		User:            c.cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(c.cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         c.cfg.Timeout,
	}

	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	sshClient := ssh.NewClient(cc, chans, reqs)

	type res struct {
		c   *sftp.Client
		err error
	}
	rch := make(chan res, 1)
	go func() {
		c, err := sftp.NewClient(sshClient)
		rch <- res{c: c, err: err}
	}()

	select {
	case <-ctx.Done():
		sshClient.Close()
		return nil, nil, ctx.Err()
	case out := <-rch:
		if out.err != nil {
			sshClient.Close()
			return nil, nil, out.err
		}
		return sshClient, out.c, nil
	case <-time.After(c.cfg.Timeout):
		sshClient.Close()
		return nil, nil, fmt.Errorf("timeout creando cliente sftp")
	}
}

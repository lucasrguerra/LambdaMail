package managesievepresentation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"lambdamail/protocols/internal/application/port"
	appusecase "lambdamail/protocols/internal/application/usecase"
)

type state int

const (
	stateAuth state = iota
	stateMain
)

type conn struct {
	rawConn     net.Conn
	reader      *bufio.Reader
	writer      *bufio.Writer
	tlsProvider port.CertProvider

	useCase *appusecase.ManageSieveSessionUseCase

	state     state
	username  string
	mailboxID string
	isTLS     bool
}

func newConn(c net.Conn, useCase *appusecase.ManageSieveSessionUseCase, tlsProvider port.CertProvider) *conn {
	return &conn{
		rawConn:     c,
		reader:      bufio.NewReader(c),
		writer:      bufio.NewWriter(c),
		useCase:     useCase,
		tlsProvider: tlsProvider,
		state:       stateAuth,
	}
}

func (c *conn) serve() {
	defer c.rawConn.Close()

	c.sendCapabilities()
	c.writeLine(`OK "LambdaMail ManageSieve ready."`)

	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToUpper(parts[0])
		arg := ""
		if len(parts) > 1 {
			arg = parts[1]
		}

		switch cmd {
		case "CAPABILITY":
			c.handleCapability()
		case "STARTTLS":
			if c.handleStartTLS() {
				c.sendCapabilities()
				c.writeLine(`OK "TLS negotiation complete."`)
			}
		case "AUTHENTICATE":
			c.handleAuthenticate(arg)
		case "PUTSCRIPT":
			c.handlePutScript(arg)
		case "GETSCRIPT":
			c.handleGetScript(arg)
		case "SETACTIVE":
			c.handleSetActive(arg)
		case "HAVESPACE":
			c.handleHaveSpace(arg)
		case "LISTSCRIPTS":
			c.handleListScripts()
		case "DELETESCRIPT":
			c.handleDeleteScript(arg)
		case "RENAMESCRIPT":
			c.handleRenameScript(arg)
		case "CHECKSCRIPT":
			c.handleCheckScript(arg)
		case "LOGOUT", "QUIT":
			c.writeLine(`OK "Logout complete."`)
			return
		default:
			c.writeLine(`NO "Unknown or unsupported command."`)
		}
	}
}

func (c *conn) writeLine(msg string) error {
	if _, err := c.writer.WriteString(msg + "\r\n"); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *conn) sendCapabilities() {
	c.writer.WriteString("\"IMPLEMENTATION\" \"LambdaMail ManageSieve\"\r\n")
	c.writer.WriteString("\"SASL\" \"PLAIN\"\r\n")
	if !c.isTLS && c.tlsProvider != nil {
		c.writer.WriteString("\"STARTTLS\"\r\n")
	}
	c.writer.WriteString("\"SIEVE\" \"fileinto reject envelope subaddress body imap4flags variables\"\r\n")
	c.writer.Flush()
}

func (c *conn) handleCapability() {
	c.sendCapabilities()
	c.writeLine(`OK "Capability completed."`)
}

func (c *conn) handleStartTLS() bool {
	if c.isTLS {
		c.writeLine(`NO "TLS already active."`)
		return false
	}
	if c.tlsProvider == nil {
		c.writeLine(`NO "STARTTLS not supported."`)
		return false
	}
	if err := c.writeLine(`OK "Begin TLS negotiation."`); err != nil {
		return false
	}
	tlsConfig := &tls.Config{
		GetCertificate: c.tlsProvider.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}
	tlsConn := tls.Server(c.rawConn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		return false
	}
	c.rawConn = tlsConn
	c.reader = bufio.NewReader(tlsConn)
	c.writer = bufio.NewWriter(tlsConn)
	c.isTLS = true
	return true
}

func (c *conn) handleAuthenticate(arg string) {
	if c.state != stateAuth {
		c.writeLine(`NO "Already authenticated."`)
		return
	}

	parts := strings.Fields(arg)
	if len(parts) == 0 || strings.ToUpper(parts[0]) != `"PLAIN"` && strings.ToUpper(parts[0]) != `PLAIN` {
		c.writeLine(`NO "Unsupported SASL mechanism."`)
		return
	}

	var respB64 string
	if len(parts) > 1 {
		respB64 = parts[1]
	} else {
		// Challenge response mode
		c.writeLine(`""`)
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return
		}
		respB64 = strings.TrimRight(line, "\r\n")
	}

	decoded, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		c.writeLine(`NO "Invalid base64 payload."`)
		return
	}

	// SASL PLAIN format: [authzid] \0 authcid \0 passwd
	saslParts := bytes.Split(decoded, []byte{0})
	var user, pass string
	if len(saslParts) == 3 {
		user = string(saslParts[1])
		pass = string(saslParts[2])
	} else if len(saslParts) == 2 {
		user = string(saslParts[0])
		pass = string(saslParts[1])
	} else {
		c.writeLine(`NO "Malformed SASL PLAIN authentication."`)
		return
	}

	ctx := context.Background()
	mailboxID, err := c.useCase.Login(ctx, user, pass)
	if err != nil {
		c.writeLine(`NO "Authentication failed."`)
		return
	}

	c.username = user
	c.mailboxID = mailboxID
	c.state = stateMain
	c.writeLine(`OK "Logged in."`)
}

func (c *conn) handlePutScript(arg string) {
	if c.state != stateMain {
		c.writeLine(`NO "Authentication required."`)
		return
	}

	name, content, err := c.parseNameAndContent(arg)
	if err != nil {
		c.writeLine(fmt.Sprintf(`NO "%v"`, err))
		return
	}

	ctx := context.Background()
	if err := c.useCase.PutScript(ctx, c.mailboxID, name, content); err != nil {
		c.writeLine(fmt.Sprintf(`NO "%v"`, err))
		return
	}

	c.writeLine(`OK "PUTSCRIPT completed."`)
}

func (c *conn) handleGetScript(arg string) {
	if c.state != stateMain {
		c.writeLine(`NO "Authentication required."`)
		return
	}

	name := c.parseStringLiteral(arg)
	if name == "" {
		c.writeLine(`NO "Missing script name."`)
		return
	}

	ctx := context.Background()
	rec, err := c.useCase.GetScript(ctx, c.mailboxID, name)
	if err != nil || rec == nil {
		c.writeLine(`NO "Script does not exist."`)
		return
	}

	c.writer.WriteString(fmt.Sprintf("{%d+}\r\n%s\r\n", len(rec.Script), rec.Script))
	c.writeLine(`OK "GETSCRIPT completed."`)
}

func (c *conn) handleSetActive(arg string) {
	if c.state != stateMain {
		c.writeLine(`NO "Authentication required."`)
		return
	}

	name := c.parseStringLiteral(arg)
	ctx := context.Background()
	if err := c.useCase.SetActive(ctx, c.mailboxID, name); err != nil {
		c.writeLine(fmt.Sprintf(`NO "%v"`, err))
		return
	}

	c.writeLine(`OK "SETACTIVE completed."`)
}

func (c *conn) handleHaveSpace(arg string) {
	parts := strings.Fields(arg)
	if len(parts) < 2 {
		c.writeLine(`NO "Missing arguments for HAVESPACE."`)
		return
	}
	size, err := strconv.Atoi(parts[1])
	if err != nil || size < 0 || size > appusecase.MaxScriptSizeBytes {
		c.writeLine(`NO "Quota exceeded."`)
		return
	}
	c.writeLine(`OK "Storage space available."`)
}

func (c *conn) handleListScripts() {
	if c.state != stateMain {
		c.writeLine(`NO "Authentication required."`)
		return
	}

	ctx := context.Background()
	scripts, err := c.useCase.ListScripts(ctx, c.mailboxID)
	if err != nil {
		c.writeLine(`NO "Unable to list scripts."`)
		return
	}

	for _, s := range scripts {
		if s.IsActive {
			c.writer.WriteString(fmt.Sprintf("%q ACTIVE\r\n", s.Name))
		} else {
			c.writer.WriteString(fmt.Sprintf("%q\r\n", s.Name))
		}
	}
	c.writeLine(`OK "LISTSCRIPTS completed."`)
}

func (c *conn) handleDeleteScript(arg string) {
	if c.state != stateMain {
		c.writeLine(`NO "Authentication required."`)
		return
	}

	name := c.parseStringLiteral(arg)
	if name == "" {
		c.writeLine(`NO "Missing script name."`)
		return
	}

	ctx := context.Background()
	if err := c.useCase.DeleteScript(ctx, c.mailboxID, name); err != nil {
		c.writeLine(fmt.Sprintf(`NO "%v"`, err))
		return
	}

	c.writeLine(`OK "DELETESCRIPT completed."`)
}

func (c *conn) handleRenameScript(arg string) {
	if c.state != stateMain {
		c.writeLine(`NO "Authentication required."`)
		return
	}

	parts := c.parseTwoStrings(arg)
	if len(parts) < 2 {
		c.writeLine(`NO "Missing script names for RENAMESCRIPT."`)
		return
	}

	ctx := context.Background()
	if err := c.useCase.RenameScript(ctx, c.mailboxID, parts[0], parts[1]); err != nil {
		c.writeLine(fmt.Sprintf(`NO "%v"`, err))
		return
	}

	c.writeLine(`OK "RENAMESCRIPT completed."`)
}

func (c *conn) handleCheckScript(arg string) {
	var content string
	var err error

	if strings.HasPrefix(arg, "{") {
		_, content, err = c.parseNameAndContent(`"" ` + arg)
	} else {
		content = c.parseStringLiteral(arg)
	}

	if err != nil {
		c.writeLine(fmt.Sprintf(`NO "%v"`, err))
		return
	}

	ctx := context.Background()
	if err := c.useCase.CheckScript(ctx, content); err != nil {
		c.writeLine(fmt.Sprintf(`NO "%v"`, err))
		return
	}

	c.writeLine(`OK "CHECKSCRIPT completed."`)
}

func (c *conn) parseStringLiteral(input string) string {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, `"`) && strings.HasSuffix(input, `"`) && len(input) >= 2 {
		return input[1 : len(input)-1]
	}
	return input
}

func (c *conn) parseTwoStrings(input string) []string {
	fields := strings.Fields(input)
	var out []string
	for _, f := range fields {
		out = append(out, c.parseStringLiteral(f))
	}
	return out
}

func (c *conn) parseNameAndContent(arg string) (string, string, error) {
	parts := strings.SplitN(arg, " ", 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("missing parameters")
	}

	name := c.parseStringLiteral(parts[0])
	literalHeader := strings.TrimSpace(parts[1])

	if !strings.HasPrefix(literalHeader, "{") || !strings.HasSuffix(literalHeader, "}") {
		// Not literal string - treat literalHeader as quoted string
		return name, c.parseStringLiteral(literalHeader), nil
	}

	sizeStr := strings.TrimPrefix(literalHeader, "{")
	sizeStr = strings.TrimSuffix(sizeStr, "}")
	sizeStr = strings.TrimSuffix(sizeStr, "+")

	size, err := strconv.Atoi(sizeStr)
	if err != nil || size < 0 {
		return "", "", fmt.Errorf("invalid literal size")
	}

	buf := make([]byte, size)
	if _, err := io.ReadFull(c.reader, buf); err != nil {
		return "", "", fmt.Errorf("unable to read script content: %w", err)
	}

	// Consume trailing CRLF
	c.reader.ReadString('\n')

	return name, string(buf), nil
}

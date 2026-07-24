package pop3presentation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"

	"lambdamail/protocols/internal/application/port"
	appusecase "lambdamail/protocols/internal/application/usecase"
)

type state int

const (
	stateAuth state = iota
	stateTransaction
	stateUpdate
)

type pop3Msg struct {
	seqNum  int
	record  port.MessageRecord
	deleted bool
}

type conn struct {
	rawConn     net.Conn
	reader      *bufio.Reader
	writer      *bufio.Writer
	tlsProvider port.CertProvider

	useCase *appusecase.Pop3SessionUseCase

	state     state
	username  string
	mailboxID string
	folderID  string
	messages  []pop3Msg
	isTLS     bool
}

func newConn(c net.Conn, useCase *appusecase.Pop3SessionUseCase, tlsProvider port.CertProvider) *conn {
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

	if err := c.writeLine("+OK LambdaMail POP3 server ready"); err != nil {
		return
	}

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
		case "CAPA":
			c.handleCapa()
		case "STLS":
			if c.handleStls() {
				// STLS succeeded, TLS handshake took over
				continue
			}
		case "USER":
			c.handleUser(arg)
		case "PASS":
			c.handlePass(arg)
		case "STAT":
			c.handleStat()
		case "LIST":
			c.handleList(arg)
		case "UIDL":
			c.handleUidl(arg)
		case "RETR":
			c.handleRetr(arg)
		case "TOP":
			c.handleTop(arg)
		case "DELE":
			c.handleDele(arg)
		case "RSET":
			c.handleRset()
		case "NOOP":
			c.handleNoop()
		case "QUIT":
			c.handleQuit()
			return
		default:
			c.writeLine("-ERR Unknown or unsupported command")
		}
	}
}

func (c *conn) writeLine(msg string) error {
	if _, err := c.writer.WriteString(msg + "\r\n"); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *conn) handleCapa() {
	c.writer.WriteString("+OK Capability list follows\r\n")
	c.writer.WriteString("USER\r\n")
	c.writer.WriteString("TOP\r\n")
	c.writer.WriteString("UIDL\r\n")
	c.writer.WriteString("RESP-CODES\r\n")
	c.writer.WriteString("PIPELINING\r\n")
	if !c.isTLS && c.tlsProvider != nil {
		c.writer.WriteString("STLS\r\n")
	}
	c.writer.WriteString("IMPLEMENTATION LambdaMail POP3\r\n")
	c.writer.WriteString(".\r\n")
	c.writer.Flush()
}

func (c *conn) handleStls() bool {
	if c.isTLS {
		c.writeLine("-ERR TLS already active")
		return false
	}
	if c.tlsProvider == nil {
		c.writeLine("-ERR STLS not supported")
		return false
	}
	if err := c.writeLine("+OK Begin TLS negotiation"); err != nil {
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

func (c *conn) handleUser(arg string) {
	if c.state != stateAuth {
		c.writeLine("-ERR Invalid command state")
		return
	}
	if arg == "" {
		c.writeLine("-ERR Missing username argument")
		return
	}
	c.username = arg
	c.writeLine("+OK User accepted")
}

func (c *conn) handlePass(password string) {
	if c.state != stateAuth || c.username == "" {
		c.writeLine("-ERR Invalid state or missing USER")
		return
	}
	ctx := context.Background()
	mailboxID, err := c.useCase.Login(ctx, c.username, password)
	if err != nil {
		c.writeLine("-ERR Invalid credentials")
		return
	}

	folder, recs, err := c.useCase.GetInbox(ctx, mailboxID)
	if err != nil {
		c.writeLine("-ERR Unable to access INBOX")
		return
	}

	c.mailboxID = mailboxID
	c.folderID = folder.ID
	c.messages = make([]pop3Msg, len(recs))
	var totalBytes int64
	for i, rec := range recs {
		c.messages[i] = pop3Msg{
			seqNum:  i + 1,
			record:  rec,
			deleted: false,
		}
		totalBytes += rec.SizeBytes
	}
	c.state = stateTransaction

	c.writeLine(fmt.Sprintf("+OK Logged in, %d message(s) (%d octets)", len(c.messages), totalBytes))
}

func (c *conn) handleStat() {
	if c.state != stateTransaction {
		c.writeLine("-ERR Invalid command state")
		return
	}
	count, totalBytes := c.statActive()
	c.writeLine(fmt.Sprintf("+OK %d %d", count, totalBytes))
}

func (c *conn) statActive() (int, int64) {
	var count int
	var totalBytes int64
	for _, msg := range c.messages {
		if !msg.deleted {
			count++
			totalBytes += msg.record.SizeBytes
		}
	}
	return count, totalBytes
}

func (c *conn) handleList(arg string) {
	if c.state != stateTransaction {
		c.writeLine("-ERR Invalid command state")
		return
	}
	if arg != "" {
		seq, err := strconv.Atoi(arg)
		if err != nil || seq < 1 || seq > len(c.messages) || c.messages[seq-1].deleted {
			c.writeLine("-ERR No such message")
			return
		}
		c.writeLine(fmt.Sprintf("+OK %d %d", seq, c.messages[seq-1].record.SizeBytes))
		return
	}

	count, totalBytes := c.statActive()
	c.writer.WriteString(fmt.Sprintf("+OK %d messages (%d octets)\r\n", count, totalBytes))
	for _, msg := range c.messages {
		if !msg.deleted {
			c.writer.WriteString(fmt.Sprintf("%d %d\r\n", msg.seqNum, msg.record.SizeBytes))
		}
	}
	c.writer.WriteString(".\r\n")
	c.writer.Flush()
}

func (c *conn) handleUidl(arg string) {
	if c.state != stateTransaction {
		c.writeLine("-ERR Invalid command state")
		return
	}
	if arg != "" {
		seq, err := strconv.Atoi(arg)
		if err != nil || seq < 1 || seq > len(c.messages) || c.messages[seq-1].deleted {
			c.writeLine("-ERR No such message")
			return
		}
		c.writeLine(fmt.Sprintf("+OK %d %d", seq, c.messages[seq-1].record.UID))
		return
	}

	c.writer.WriteString("+OK Unique-ID listing follows\r\n")
	for _, msg := range c.messages {
		if !msg.deleted {
			c.writer.WriteString(fmt.Sprintf("%d %d\r\n", msg.seqNum, msg.record.UID))
		}
	}
	c.writer.WriteString(".\r\n")
	c.writer.Flush()
}

func (c *conn) handleRetr(arg string) {
	if c.state != stateTransaction {
		c.writeLine("-ERR Invalid command state")
		return
	}
	seq, err := strconv.Atoi(arg)
	if err != nil || seq < 1 || seq > len(c.messages) || c.messages[seq-1].deleted {
		c.writeLine("-ERR No such message")
		return
	}

	msg := &c.messages[seq-1]
	ctx := context.Background()
	content, err := c.useCase.ReadBlob(ctx, msg.record.BlobID)
	if err != nil {
		c.writeLine("-ERR Unable to read message content")
		return
	}

	c.writer.WriteString(fmt.Sprintf("+OK %d octets\r\n", len(content)))
	c.writeDotStuffedContent(content)
	c.writer.WriteString(".\r\n")
	c.writer.Flush()
}

func (c *conn) handleTop(arg string) {
	if c.state != stateTransaction {
		c.writeLine("-ERR Invalid command state")
		return
	}
	parts := strings.Fields(arg)
	if len(parts) < 2 {
		c.writeLine("-ERR Missing parameters for TOP")
		return
	}
	seq, err := strconv.Atoi(parts[0])
	numLines, err2 := strconv.Atoi(parts[1])
	if err != nil || err2 != nil || seq < 1 || seq > len(c.messages) || c.messages[seq-1].deleted || numLines < 0 {
		c.writeLine("-ERR Invalid message number or line count")
		return
	}

	msg := &c.messages[seq-1]
	ctx := context.Background()
	content, err := c.useCase.ReadBlob(ctx, msg.record.BlobID)
	if err != nil {
		c.writeLine("-ERR Unable to read message content")
		return
	}

	c.writer.WriteString("+OK Top of message follows\r\n")
	topContent := extractTopLines(content, numLines)
	c.writeDotStuffedContent(topContent)
	c.writer.WriteString(".\r\n")
	c.writer.Flush()
}

func extractTopLines(content []byte, n int) []byte {
	// Separate header and body by \r\n\r\n or \n\n
	sepIdx := bytes.Index(content, []byte("\r\n\r\n"))
	sepLen := 4
	if sepIdx == -1 {
		sepIdx = bytes.Index(content, []byte("\n\n"))
		sepLen = 2
	}

	if sepIdx == -1 {
		// Entire content treated as header
		return content
	}

	header := content[:sepIdx+sepLen]
	body := content[sepIdx+sepLen:]

	var buf bytes.Buffer
	buf.Write(header)

	bodyScanner := bufio.NewScanner(bytes.NewReader(body))
	linesWritten := 0
	for bodyScanner.Scan() && linesWritten < n {
		buf.WriteString(bodyScanner.Text() + "\r\n")
		linesWritten++
	}
	return buf.Bytes()
}

func (c *conn) writeDotStuffedContent(content []byte) {
	s := bufio.NewScanner(bytes.NewReader(content))
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, ".") {
			c.writer.WriteString("." + line + "\r\n")
		} else {
			c.writer.WriteString(line + "\r\n")
		}
	}
}

func (c *conn) handleDele(arg string) {
	if c.state != stateTransaction {
		c.writeLine("-ERR Invalid command state")
		return
	}
	seq, err := strconv.Atoi(arg)
	if err != nil || seq < 1 || seq > len(c.messages) || c.messages[seq-1].deleted {
		c.writeLine("-ERR Message already deleted or no such message")
		return
	}

	c.messages[seq-1].deleted = true
	c.writeLine(fmt.Sprintf("+OK Message %d marked for deletion", seq))
}

func (c *conn) handleRset() {
	if c.state != stateTransaction {
		c.writeLine("-ERR Invalid command state")
		return
	}
	for i := range c.messages {
		c.messages[i].deleted = false
	}
	c.writeLine("+OK Mailbox reset")
}

func (c *conn) handleNoop() {
	c.writeLine("+OK")
}

func (c *conn) handleQuit() {
	if c.state == stateAuth {
		c.writeLine("+OK POP3 server signing off")
		return
	}

	if c.state == stateTransaction {
		c.state = stateUpdate
		var toDelete []uint32
		for _, msg := range c.messages {
			if msg.deleted {
				toDelete = append(toDelete, msg.record.UID)
			}
		}
		if len(toDelete) > 0 {
			ctx := context.Background()
			_ = c.useCase.ExpungeMessages(ctx, c.folderID, toDelete)
		}
		c.writeLine("+OK POP3 server signing off")
	}
}

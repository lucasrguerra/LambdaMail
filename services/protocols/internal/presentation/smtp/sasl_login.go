package smtppresentation

import (
	"errors"

	"github.com/emersion/go-sasl"
)

// loginMechanism is the SASL mechanism name for the legacy LOGIN exchange.
// PLAN.md section 4 requires it alongside PLAIN because several desktop and
// mobile clients still offer nothing else. Neither go-sasl nor go-smtp ships a
// server side for it, so it is implemented here.
const loginMechanism = "LOGIN"

// LOGIN is not specified by an RFC; it is a de facto mechanism where the
// server prompts for the username, then for the password, each as a bare
// challenge string.
var (
	loginUsernameChallenge = []byte("Username:")
	loginPasswordChallenge = []byte("Password:")
)

// loginAuthenticator verifies a username and password pair.
type loginAuthenticator func(username, password string) error

// loginServer implements sasl.Server for the LOGIN mechanism.
type loginServer struct {
	authenticate loginAuthenticator
	username     string
	// state advances from awaiting the username, to awaiting the password,
	// to finished.
	state int
}

const (
	loginStateAwaitingUsername = iota
	loginStateAwaitingPassword
	loginStateDone
)

func newLoginServer(authenticate loginAuthenticator) sasl.Server {
	return &loginServer{authenticate: authenticate}
}

func (s *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch s.state {
	case loginStateAwaitingUsername:
		// A client may send the username as the initial response, in which
		// case there is nothing to prompt for yet.
		if response == nil {
			return loginUsernameChallenge, false, nil
		}
		s.username = string(response)
		s.state = loginStateAwaitingPassword
		return loginPasswordChallenge, false, nil

	case loginStateAwaitingPassword:
		s.state = loginStateDone
		if err := s.authenticate(s.username, string(response)); err != nil {
			return nil, false, err
		}
		return nil, true, nil

	default:
		return nil, false, errors.New("sasl: LOGIN exchange already finished")
	}
}

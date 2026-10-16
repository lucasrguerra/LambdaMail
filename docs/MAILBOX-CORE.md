# Mailbox core

The webmail, IMAP, POP3 and SMTP are four ways in to one mailbox. They share
the rules and the data code; none of them has its own copy.

## Where each rule lives

| Rule | One place |
|------|-----------|
| Flags, expunge, move, copy, folder delete, counters, quota | `MailStore` (`services/protocols/internal/infrastructure/postgres/mail_store.go`) |
| Folder and message reads | `mail_store_read.go` (same store) |
| Credentials (password, app password, scopes) | `usecase/credentials.go` |
| Sending, and the copy filed in Sent | `ProcessOutboundEmail.Submit` (`FileSentCopy`) |
| Default folders of a mailbox | SQL function `provision_default_folders` (migration 0014) |

The IMAP repositories, the webmail repository and the TS auth service are thin
adapters: they translate their protocol and call the code above.

## Invariants

After any operation, through any port:

- `folders.total_count` and `unread_count` equal the live messages and the ones
  without `\Seen`.
- `folders.highest_modseq` rises only when a message really changed.
- `mailboxes.used_bytes` equals the size of the live messages.
- Deleting twice, or from two ports at once, counts once.

`mail_state_contract_test.go` checks all of this after every operation through
the IMAP path and the webmail path, and under concurrency.
`e2e/one_mailbox_everywhere_e2e_test.go` does the same over real SMTP, IMAP and
HTTP on one mailbox.

## Lock order

Every operation locks **folders first, lowest id first, then the message**
(`lockFolders`). Two moves in opposite directions, or a move racing a flag
change, used to deadlock (SQLSTATE 40P01) because they locked in different
orders. `TestContract_OppositeMovesAndFlagChangesNeverDeadlock` covers it. A new
operation must follow the same order.

## Adding an operation

1. Put it in `MailStore`, in one transaction, with `lockFolders`.
2. Make the adapters call it.
3. Add it to the contract test and run it through both paths.

## Migration 0014

Creates `provision_default_folders`, creates the missing default folders of
existing mailboxes, and recomputes folder counters and `used_bytes` from the
messages. It is idempotent and runs with the other migrations at deploy.

## Not done

IMAP `APPEND` is still a stub. When it is implemented, drop a client-appended
Sent copy that matches the server's one by `Message-ID`.

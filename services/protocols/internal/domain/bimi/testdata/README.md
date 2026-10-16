Real Verified Mark Certificates, used only to test the parser.

Both are public: a BIMI certificate is published on purpose at the URL in the
`a=` tag of the sender's `default._bimi` DNS record, so that any mail provider
can fetch it.

| Files | Published by | Fetched from |
|-------|--------------|--------------|
| `ebay.pem`, `ebay.svg` | eBay, issued by DigiCert | `default._bimi.ebay.com` (2026-10-16) |
| `cnn.pem`, `cnn.svg`   | CNN, issued through Valimail | `default._bimi.cnn.com` (2026-10-16) |

The tests pin the clock to 2026-10-16, inside both validity periods. They hold
the format the parser has to read: a leaf certificate with the "Brand Indicator
for Message Identification" extended key usage and a logotype extension whose
data URI is the logo, gzipped and base64 encoded.

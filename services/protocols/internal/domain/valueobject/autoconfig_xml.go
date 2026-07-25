package valueobject

import (
	"fmt"
)

// BuildThunderbirdAutoconfigXML generates Mozilla Thunderbird config-v1.1 XML payload.
func BuildThunderbirdAutoconfigXML(domainName, mailHost string) string {
	if mailHost == "" {
		mailHost = fmt.Sprintf("mail.%s", domainName)
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<clientConfig version="1.1">
  <emailProvider id="%s">
    <domain>%s</domain>
    <displayName>LambdaMail - %s</displayName>
    <displayShortName>%s</displayShortName>
    <incomingServer type="imap">
      <hostname>%s</hostname>
      <port>993</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>%%EMAILADDRESS%%</username>
    </incomingServer>
    <incomingServer type="pop3">
      <hostname>%s</hostname>
      <port>995</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>%%EMAILADDRESS%%</username>
    </incomingServer>
    <outgoingServer type="smtp">
      <hostname>%s</hostname>
      <port>587</port>
      <socketType>STARTTLS</socketType>
      <authentication>password-cleartext</authentication>
      <username>%%EMAILADDRESS%%</username>
    </outgoingServer>
  </emailProvider>
</clientConfig>`, domainName, domainName, domainName, domainName, mailHost, mailHost, mailHost)
}

// BuildOutlookAutodiscoverXML generates Microsoft POX Autodiscover response XML.
func BuildOutlookAutodiscoverXML(domainName, mailHost, emailAddress string) string {
	if mailHost == "" {
		mailHost = fmt.Sprintf("mail.%s", domainName)
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006">
  <Response xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a">
    <User>
      <DisplayName>%s</DisplayName>
    </User>
    <Account>
      <AccountType>email</AccountType>
      <Action>settings</Action>
      <Protocol>
        <Type>IMAP</Type>
        <Server>%s</Server>
        <Port>993</Port>
        <SSL>on</SSL>
        <AuthRequired>on</AuthRequired>
        <LoginName>%s</LoginName>
      </Protocol>
      <Protocol>
        <Type>SMTP</Type>
        <Server>%s</Server>
        <Port>587</Port>
        <SSL>off</SSL>
        <Encryption>TLS</Encryption>
        <AuthRequired>on</AuthRequired>
        <LoginName>%s</LoginName>
      </Protocol>
    </Account>
  </Response>
</Autodiscover>`, emailAddress, mailHost, emailAddress, mailHost, emailAddress)
}

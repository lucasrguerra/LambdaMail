# DNS resolution is needed for RBL, SPF and DKIM lookups.
dns {
  nameserver = ["1.1.1.1", "8.8.8.8"];
  timeout = 3s;
}

#!/usr/bin/env python3
"""Supervised offline certificate operations and read-only management TLS checks."""

import argparse
import os
from pathlib import Path
import re
import secrets
import socket
import ssl
import subprocess
import sys
import tempfile
import time

def openssl(*args):
    result = subprocess.run(["openssl", *map(str, args)], capture_output=True)
    if result.returncode:
        # OpenSSL diagnostics can include private paths or certificate contents.
        raise ValueError("OpenSSL operation failed; check inputs, password, chain and expiry")
    return result.stdout

def private_output(value):
    path = Path(value).absolute()
    if path != path.resolve():
        raise ValueError("output must not contain symlinks")
    if any((parent / ".git").exists() for parent in (path, *path.parents)):
        raise ValueError("output must be outside Git checkouts")
    if path.exists():
        raise ValueError("output already exists; use a fresh path")
    if not path.parent.is_dir():
        raise ValueError("output parent must already exist")
    return path

def password_file(value):
    path = Path(value)
    if not path.is_file() or path.stat().st_mode & 0o077 or not path.read_bytes().strip():
        raise ValueError("password file must be nonempty and private (mode 0600)")
    return "file:" + str(path.resolve())

def dns_name(value):
    if (len(value) > 253 or "." not in value
            or any(not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label)
                   for label in value.split("."))):
        raise argparse.ArgumentTypeError("use a lowercase fully qualified DNS name without wildcards")
    return value

def nonnegative(value):
    number = int(value)
    if number < 0:
        raise argparse.ArgumentTypeError("value must be nonnegative")
    return number

def root(args):
    target = private_output(args.output)
    password = password_file(args.password_file)
    target.mkdir(mode=0o700)
    openssl("genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:3072",
            "-aes-256-cbc", "-pass", password, "-out", target / "root.key")
    openssl("req", "-new", "-x509", "-sha256", "-days", "3650",
            "-key", target / "root.key", "-passin", password,
            "-subj", "/CN=Home Site Management Root",
            "-addext", "basicConstraints=critical,CA:TRUE,pathlen:0",
            "-addext", "keyUsage=critical,keyCertSign,cRLSign",
            "-addext", "subjectKeyIdentifier=hash", "-out", target / "root.crt")

def request(args):
    target = private_output(args.output)
    target.mkdir(mode=0o700)
    openssl("req", "-new", "-newkey", "rsa:2048", "-noenc", "-sha256",
            "-subj", "/CN=" + args.name, "-addext", "subjectAltName=DNS:" + args.name,
            "-keyout", target / "leaf.key", "-out", target / "leaf.csr")

def sign(args):
    target = private_output(args.output)
    ca = Path(args.ca)
    password = password_file(args.password_file)
    openssl("x509", "-in", ca / "root.crt", "-noout", "-checkend", str(366 * 86400))
    openssl("req", "-in", args.request, "-noout", "-verify")
    target.mkdir(mode=0o700)
    with tempfile.TemporaryDirectory(prefix="home-ca-sign-") as temporary:
        extensions = Path(temporary) / "extensions"
        extensions.write_text(
            "basicConstraints=critical,CA:FALSE\n"
            "keyUsage=critical,digitalSignature,keyEncipherment\n"
            "extendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\n"
            "authorityKeyIdentifier=keyid,issuer\nsubjectAltName=DNS:" + args.name + "\n")
        openssl("x509", "-req", "-in", args.request, "-CA", ca / "root.crt",
                "-CAkey", ca / "root.key", "-passin", password,
                "-set_serial", "0x" + secrets.token_hex(16), "-days", "365", "-sha256",
                "-extfile", extensions, "-out", target / "leaf.crt")
    verify_certificate(target / "leaf.crt", ca / "root.crt", args.name, 30)

def verify_certificate(cert, ca, name, days):
    openssl("verify", "-CAfile", ca, "-no-CApath", "-no-CAstore",
            "-purpose", "sslserver", "-verify_hostname", name, cert)
    openssl("x509", "-in", cert, "-noout", "-checkend", str(days * 86400))
    openssl("x509", "-in", ca, "-noout", "-checkend", str(days * 86400))


def bundle(args):
    target = private_output(args.output)
    password = password_file(args.password_file)
    verify_certificate(args.cert, args.ca_cert, args.name, 30)
    key_public = openssl("pkey", "-in", args.key, "-pubout")
    cert_public = openssl("x509", "-in", args.cert, "-pubkey", "-noout")
    if key_public != cert_public:
        raise ValueError("leaf certificate does not match the private key")
    # Create exclusively so an existing file cannot be truncated.
    with target.open("xb") as output:
        output.write(openssl("pkcs12", "-export", "-inkey", args.key,
                             "-in", args.cert, "-certfile", args.ca_cert,
                             "-passout", password, "-name", args.name))


def probe(args):
    context = ssl.create_default_context(cafile=args.ca_cert)
    with socket.create_connection((args.address or args.name, args.port), timeout=10) as connection:
        with context.wrap_socket(connection, server_hostname=args.name) as secure:
            expires = ssl.cert_time_to_seconds(secure.getpeercert()["notAfter"])
            if args.expected_cert:
                expected = ssl.PEM_cert_to_DER_cert(Path(args.expected_cert).read_text())
                if secure.getpeercert(binary_form=True) != expected:
                    raise ValueError("endpoint is not serving the expected leaf certificate")
    if expires <= time.time() + args.min_days * 86400:
        raise ValueError("endpoint certificate expires inside the requested threshold")
    openssl("x509", "-in", args.ca_cert, "-noout", "-checkend", str(args.min_days * 86400))

def verify(args):
    verify_certificate(args.cert, args.ca_cert, args.name, args.min_days)


def parser():
    cli = argparse.ArgumentParser(description=__doc__)
    commands = cli.add_subparsers(dest="command", required=True)
    arguments = {
        "output": {"required": True},
        "password-file": {"required": True},
        "name": {"required": True, "type": dns_name},
        "ca": {"required": True},
        "request": {"required": True},
        "ca-cert": {"required": True},
        "cert": {"required": True},
        "key": {"required": True},
        "min-days": {"type": nonnegative, "default": 30},
        "expected-cert": {"help": "require this exact PEM leaf after replacement"},
        "address": {"help": "connect address; DNS name still used for SNI and verification"},
        "port": {"type": int, "default": 53443},
    }
    operations = {
        "root": (root, ("output", "password-file")),
        "request": (request, ("name", "output")),
        "sign": (sign, ("name", "ca", "request", "output", "password-file")),
        "verify": (verify, ("name", "ca-cert", "cert", "min-days")),
        "bundle": (bundle, ("name", "key", "cert", "ca-cert", "password-file", "output")),
        "probe": (probe, ("name", "ca-cert", "address", "port", "min-days", "expected-cert")),
    }
    for name, (handler, options) in operations.items():
        command = commands.add_parser(name)
        command.set_defaults(handler=handler)
        for option in options:
            command.add_argument("--" + option, **arguments[option])
    return cli


def main():
    os.umask(0o077)
    args = parser().parse_args()
    try:
        args.handler(args)
    except OSError:
        print("FAILED: file or TLS connection unavailable; check private inputs and trust", file=sys.stderr)
        return 1
    except ValueError as error:
        print("FAILED: " + str(error), file=sys.stderr)
        return 1
    print("OK: " + args.command)
    return 0


if __name__ == "__main__":
    sys.exit(main())

"""Reconcile one dedicated Unix and Samba login from a credential on stdin."""

import grp
import json
import os
import pwd
import re
import subprocess
import sys

READER_GROUP = "smb-media-readers"
READER_HOME = "/nonexistent"
READER_SHELL = "/usr/sbin/nologin"


def ensure_unix_reader(username):
    gid = grp.getgrnam(READER_GROUP).gr_gid
    try:
        user = pwd.getpwnam(username)
    except KeyError:
        subprocess.run(
            [
                "/usr/sbin/useradd", "--no-create-home", "--home-dir", READER_HOME,
                "--shell", READER_SHELL, "--gid", READER_GROUP, username,
            ],
            check=True,
        )
        return True

    if (
        user.pw_uid == 0
        or user.pw_gid != gid
        or user.pw_dir != READER_HOME
        or user.pw_shell != READER_SHELL
        or set(os.getgrouplist(username, gid)) != {gid}
    ):
        raise ValueError("The existing Unix account is not a dedicated Samba reader.")
    return False


def samba_record(username):
    result = subprocess.run(
        ["/usr/bin/pdbedit", "-L", "-w", "-u", username],
        env={**os.environ, "LC_ALL": "C"}, capture_output=True,
    )
    if result.returncode == 255 and result.stderr.strip() == b"Username not found!":
        return None
    result.check_returncode()
    record = result.stdout.strip().split(b":")
    if len(record) != 7 or record[0] != username.encode("ascii"):
        raise ValueError("Samba returned an unexpected account record.")
    return record[3].lower(), record[4]


def ensure_samba_password(username, password):
    # Samba stores the NT password hash as MD4 of the UTF-16LE password.
    hash_result = subprocess.run(
        ["/usr/bin/openssl", "dgst", "-provider", "legacy", "-md4", "-binary"],
        input=password.encode("utf-16-le"), capture_output=True, check=True,
    )
    password_hash = hash_result.stdout.hex().encode("ascii")

    existing = samba_record(username)
    if existing is not None and existing[0] == password_hash and b"D" not in existing[1]:
        return False

    subprocess.run(
        ["/usr/bin/smbpasswd", "-s", "-a", username],
        input=f"{password}\n{password}\n", encoding="utf-8", capture_output=True, check=True,
    )
    updated = samba_record(username)
    if updated is None or updated[0] != password_hash or b"D" in updated[1]:
        raise ValueError("Samba did not apply the declared password.")
    return True


def main():
    account = json.load(sys.stdin)
    username = account["username"]
    password = account["password"]
    if (
        not isinstance(username, str)
        or not re.fullmatch(r"smb-[a-z0-9][a-z0-9_-]{0,27}", username)
        or username in {"smb-media", READER_GROUP}
    ):
        raise ValueError("Use a dedicated smb- username for the Samba reader.")
    # smbpasswd -s uses a 256-byte buffer including the newline and terminator.
    if (
        not isinstance(password, str)
        or not 1 <= len(password.encode("utf-8")) <= 254
        or any(character in password for character in "\0\r\n")
    ):
        raise ValueError("Use a single-line Samba password of 1 to 254 UTF-8 bytes.")

    unix_changed = ensure_unix_reader(username)
    password_changed = ensure_samba_password(username, password)
    print("changed" if unix_changed or password_changed else "ok")


if __name__ == "__main__":
    main()

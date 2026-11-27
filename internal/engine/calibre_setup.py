"""Bootstrap the pinned Calibre-Web Automated image before its port is published."""
import os
from pathlib import Path
import shutil
import sqlite3
import sys
import tempfile

from werkzeug.security import generate_password_hash

app = Path("/app/calibre-web-automated")
sys.path.insert(0, str(app / "scripts"))
from cwa_db import CWA_DB

config = Path("/config")
def formats(value):
    return {f.strip().lower() for f in value.split(",") if f.strip()}


def conversion_policy(connection):
    current = connection.execute(
        "SELECT auto_convert, auto_convert_target_format, auto_convert_retained_formats, "
        "auto_convert_ignored_formats, auto_ingest_ignored_formats FROM cwa_settings"
    ).fetchone()
    _, _, retained, ignore_convert, ignore_ingest = current
    desired = (1, "azw3", ",".join(sorted(formats(retained) | {"epub"})),
               ",".join(sorted(formats(ignore_convert) - {"epub"})),
               ",".join(sorted(formats(ignore_ingest) - {"epub"})))
    return current, desired


observe = sys.argv[1:] == ["observe"]
uid, gid = int(os.environ["PUID"]), int(os.environ["PGID"])
if not observe:
    os.chown(config, uid, gid)
os.setgroups([])
os.setgid(gid)
os.setuid(uid)
if observe:
    with sqlite3.connect("file:/config/cwa.db?mode=ro", uri=True) as con:
        current, desired = conversion_policy(con)
        print("calibre-ready" if current == desired else "drift")
    sys.exit(0)

password = sys.stdin.read()
if not password:
    raise ValueError("missing household password")

db = config / "app.db"
fresh = not db.exists()
if fresh:
    # Publish a fully credentialed database. A retry after a failed first run
    # cannot mistake the image's example database for an initialized account.
    with tempfile.TemporaryDirectory(dir=config) as tmp:
        target = Path(tmp) / "app.db"
        shutil.copyfile(app / "empty_library/app.db", target)
        with sqlite3.connect(target) as con:
            changed = con.execute("UPDATE user SET password = ? WHERE id = 1",
                                  (generate_password_hash(password),))
            if changed.rowcount != 1:
                raise ValueError("missing initial local account")
            con.execute("DELETE FROM flask_settings")
            con.execute("UPDATE settings SET config_anonbrowse=0, config_public_reg=0, config_login_type=0")
        os.replace(target, db)

settings = CWA_DB()
with settings.con as con:
    current, desired = conversion_policy(con)
    if current != desired:
        con.execute("UPDATE cwa_settings SET auto_convert=?, auto_convert_target_format=?, "
                    "auto_convert_retained_formats=?, auto_convert_ignored_formats=?, auto_ingest_ignored_formats=?",
                    desired)
print("calibre-ready")

#!/usr/bin/env python3
"""静默全屏截图（XDG Desktop Portal，无声音无动画）"""
import sys
import time
import shutil
import urllib.parse

import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gio, GLib  # noqa: E402

OUT = sys.argv[1] if len(sys.argv) > 1 else "/tmp/portal_shot.png"


def main():
    conn = Gio.bus_get_sync(Gio.BusType.SESSION, None)
    token = "hermes_shot_%d" % int(time.time() * 1000)

    def on_response(conn2, sender, path, iface, sig, params):
        try:
            resp, results = params.unpack()
        except Exception:
            return
        if resp == 0:
            uri = results.get("uri", "")
            f = urllib.parse.unquote(uri.replace("file://", ""))
            shutil.copy(f, OUT)
            try:
                os.remove(f)
            except Exception:
                pass
            print("OK " + OUT)
        else:
            print("ERR resp=%d" % resp)
        Gtk.main_quit()

    conn.signal_subscribe(
        "org.freedesktop.portal.Desktop",
        "org.freedesktop.portal.Request",
        "Response",
        None,
        None,
        Gio.DBusSignalFlags.NONE,
        on_response,
    )

    opts = {"handle_token": GLib.Variant("s", token)}
    try:
        conn.call_sync(
            "org.freedesktop.portal.Desktop",
            "/org/freedesktop/portal/desktop",
            "org.freedesktop.portal.Screenshot",
            "Screenshot",
            GLib.Variant("(sa{sv})", ("", opts)),
            GLib.VariantType.new("(o)"),
            Gio.DBusCallFlags.NONE,
            10000,
            None,
        )
    except GLib.Error as e:
        print("ERR call: %s" % e.message)
        return

    # 等待 Response 信号（最多 15 秒）
    GLib.timeout_add_seconds(15, Gtk.main_quit)
    Gtk.main()


import os  # noqa: E402

if __name__ == "__main__":
    main()

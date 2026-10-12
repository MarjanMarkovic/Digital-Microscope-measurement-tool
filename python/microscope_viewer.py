"""Microscope Viewer - live UVC camera view with calibrated measurement tools.

Dependencies: Python 3.9+, opencv-python, numpy, Pillow (Tkinter ships with Python).
Run:  python microscope_viewer.py
"""
import csv
import json
import math
import os
import sys
import threading
import time
import tkinter as tk
from datetime import datetime
from tkinter import filedialog, messagebox, ttk

import cv2
import numpy as np
from PIL import Image, ImageDraw, ImageFont, ImageTk

APP_DIR = os.path.dirname(os.path.abspath(__file__))
CAL_FILE = os.path.join(APP_DIR, "calibrations.json")
# DirectShow opens UVC cameras fastest on Windows; elsewhere let OpenCV choose.
CAM_BACKEND = cv2.CAP_DSHOW if sys.platform == "win32" else cv2.CAP_ANY

COL_SHAPE = "#00e676"
COL_SEL = "#ffeb3b"
COL_ACTIVE = "#40c4ff"
COL_CAL = "#ff9800"

NEED = {"line": 2, "circle": 2, "rect": 2, "calibrate": 2, "angle": 3, "polyline": None}
TYPE_NAME = {"line": "Distance", "polyline": "Path", "angle": "Angle",
             "circle": "Circle", "rect": "Rectangle", "calibrate": "Calibration"}
RESOLUTIONS = ["Maximum available", "Camera default", "640x480", "800x600", "1280x720", "1280x960", "1600x1200",
               "1920x1080", "2048x1536", "2592x1944", "2560x1440", "3840x2160"]


# ----------------------------------------------------------------------------- formatting
def fmt_len(px, ppm):
    if not ppm:
        return f"{px:.1f} px"
    mm = px / ppm
    if abs(mm) < 1:
        return f"{mm * 1000:.1f} µm"
    return f"{mm:.3f} mm"


def fmt_area(px2, ppm):
    if not ppm:
        return f"{px2:.0f} px²"
    mm2 = px2 / ppm ** 2
    if mm2 < 0.01:
        return f"{mm2 * 1e6:.0f} µm²"
    return f"{mm2:.4f} mm²"


def dist(a, b):
    return math.hypot(a[0] - b[0], a[1] - b[1])


def seg_dist(p, a, b):
    ax, ay = a
    bx, by = b
    dx, dy = bx - ax, by - ay
    L2 = dx * dx + dy * dy
    if L2 == 0:
        return dist(p, a)
    t = max(0.0, min(1.0, ((p[0] - ax) * dx + (p[1] - ay) * dy) / L2))
    return math.hypot(p[0] - (ax + t * dx), p[1] - (ay + t * dy))


# ----------------------------------------------------------------------------- shapes
class Shape:
    def __init__(self, kind, pts):
        self.kind = kind
        self.pts = [list(p) for p in pts]

    def angle_deg(self):
        a, v, c = self.pts[0], self.pts[1], self.pts[2]
        u = (a[0] - v[0], a[1] - v[1])
        w = (c[0] - v[0], c[1] - v[1])
        n = math.hypot(*u) * math.hypot(*w)
        if n == 0:
            return 0.0
        return math.degrees(math.acos(max(-1.0, min(1.0, (u[0] * w[0] + u[1] * w[1]) / n))))

    def info(self, ppm):
        """Return (overlay label, detail text)."""
        k, P = self.kind, self.pts
        if k == "calibrate":
            return f"{dist(P[0], P[1]):.2f} px", ""
        if k == "line":
            L = dist(P[0], P[1])
            ang = math.degrees(math.atan2(-(P[1][1] - P[0][1]), P[1][0] - P[0][0])) + 0.0
            return fmt_len(L, ppm), f"Δx {fmt_len(abs(P[1][0] - P[0][0]), ppm)}, " \
                                    f"Δy {fmt_len(abs(P[1][1] - P[0][1]), ppm)}, angle {ang:.1f}°"
        if k == "polyline":
            L = sum(dist(P[i], P[i + 1]) for i in range(len(P) - 1))
            return fmt_len(L, ppm), f"{max(len(P) - 1, 0)} segments"
        if k == "angle":
            if len(P) < 3:
                return "", ""
            return f"{self.angle_deg():.1f}°", ""
        if k == "circle":
            r = dist(P[0], P[1])
            return f"Ø {fmt_len(2 * r, ppm)}", f"radius {fmt_len(r, ppm)}, area {fmt_area(math.pi * r * r, ppm)}"
        if k == "rect":
            w, h = abs(P[1][0] - P[0][0]), abs(P[1][1] - P[0][1])
            return f"{fmt_len(w, ppm)} × {fmt_len(h, ppm)}", \
                f"area {fmt_area(w * h, ppm)}, perimeter {fmt_len(2 * (w + h), ppm)}"
        return "", ""

    def degenerate(self):
        k, P = self.kind, self.pts
        if k == "polyline":
            return len(P) < 2 or all(dist(P[0], q) < 1e-6 for q in P)
        if k == "angle":
            return len(P) < 3 or dist(P[0], P[1]) < 1e-6 or dist(P[1], P[2]) < 1e-6
        return dist(P[0], P[1]) < 1e-6


# ----------------------------------------------------------------------------- painters
def _load_font(size):
    for name in ("segoeui.ttf", "arial.ttf", "DejaVuSans.ttf"):
        try:
            return ImageFont.truetype(name, size)
        except OSError:
            continue
    return ImageFont.load_default()


class CanvasPainter:
    def __init__(self, cv, tag="ov"):
        self.cv, self.tag = cv, tag

    def line(self, pts, color, w, halo=True):
        flat = [c for p in pts for c in p]
        if halo:
            self.cv.create_line(*flat, fill="#000000", width=w + 2, tags=self.tag)
        self.cv.create_line(*flat, fill=color, width=w, tags=self.tag)

    def oval(self, cx, cy, r, color, w):
        for c, ww in (("#000000", w + 2), (color, w)):
            self.cv.create_oval(cx - r, cy - r, cx + r, cy + r, outline=c, width=ww, tags=self.tag)

    def rect(self, x0, y0, x1, y1, color, w, fill=None):
        if fill is None:
            self.cv.create_rectangle(x0, y0, x1, y1, outline="#000000", width=w + 2, tags=self.tag)
        self.cv.create_rectangle(x0, y0, x1, y1, outline=color, width=w, fill=fill or "", tags=self.tag)

    def arc(self, cx, cy, r, start, extent, color, w):
        for c, ww in (("#000000", w + 2), (color, w)):
            self.cv.create_arc(cx - r, cy - r, cx + r, cy + r, start=start, extent=extent,
                               style="arc", outline=c, width=ww, tags=self.tag)

    def text(self, x, y, s, color, size, anchor="nw", bg="#101010", pad=3):
        t = self.cv.create_text(x, y, text=s, fill=color, anchor=anchor,
                                font=("Segoe UI", -int(size), "bold"), tags=self.tag)
        if bg:
            x0, y0, x1, y1 = self.cv.bbox(t)
            r = self.cv.create_rectangle(x0 - pad, y0 - pad, x1 + pad, y1 + pad,
                                         fill=bg, outline="", tags=self.tag)
            self.cv.tag_lower(r, t)


class PILPainter:
    def __init__(self, img, ui):
        self.d = ImageDraw.Draw(img)
        self.ui = ui
        self._fonts = {}

    def _font(self, size):
        size = int(size)
        if size not in self._fonts:
            self._fonts[size] = _load_font(size)
        return self._fonts[size]

    def line(self, pts, color, w, halo=True):
        pts = [tuple(p) for p in pts]
        if halo:
            self.d.line(pts, fill="#000000", width=w + 2, joint="curve")
        self.d.line(pts, fill=color, width=w, joint="curve")

    def oval(self, cx, cy, r, color, w):
        self.d.ellipse([cx - r, cy - r, cx + r, cy + r], outline="#000000", width=w + 2)
        self.d.ellipse([cx - r, cy - r, cx + r, cy + r], outline=color, width=w)

    def rect(self, x0, y0, x1, y1, color, w, fill=None):
        box = [min(x0, x1), min(y0, y1), max(x0, x1), max(y0, y1)]
        self.d.rectangle(box, outline="#000000", width=w + 2)
        self.d.rectangle(box, outline=color, width=w, fill=fill)

    def arc(self, cx, cy, r, start, extent, color, w):
        lo, hi = min(start, start + extent), max(start, start + extent)
        box = [cx - r, cy - r, cx + r, cy + r]
        self.d.arc(box, -hi, -lo, fill="#000000", width=w + 2)
        self.d.arc(box, -hi, -lo, fill=color, width=w)

    def text(self, x, y, s, color, size, anchor="nw", bg="#101010", pad=3):
        f = self._font(size)
        x0, y0, x1, y1 = self.d.textbbox((0, 0), s, font=f)
        tw, th = x1 - x0, y1 - y0
        if anchor == "sw":
            y -= th
        px, py = x - x0, y - y0
        if bg:
            self.d.rectangle([x - pad, y - pad, x + tw + pad, y + th + pad], fill=bg)
        self.d.text((px, py), s, fill=color, font=f)


def draw_shape(P, sh, m, ui, ppm, color, labels=True, handles=False):
    pts = [m(p) for p in sh.pts]
    k = sh.kind
    w = max(1, int(round(2 * ui)))
    anchor = None
    if k in ("line", "calibrate"):
        a, b = pts
        P.line([a, b], color, w)
        L = math.hypot(b[0] - a[0], b[1] - a[1])
        if L > 0:
            nx, ny, t = -(b[1] - a[1]) / L, (b[0] - a[0]) / L, 7 * ui
            for q in (a, b):
                P.line([(q[0] - nx * t, q[1] - ny * t), (q[0] + nx * t, q[1] + ny * t)], color, w)
        anchor = ((a[0] + b[0]) / 2, (a[1] + b[1]) / 2)
    elif k == "polyline":
        if len(pts) >= 2:
            P.line(pts, color, w)
        for q in pts:
            P.line([(q[0] - 4 * ui, q[1]), (q[0] + 4 * ui, q[1])], color, w)
            P.line([(q[0], q[1] - 4 * ui), (q[0], q[1] + 4 * ui)], color, w)
        anchor = pts[-1]
    elif k == "angle":
        if len(pts) >= 2:
            P.line(pts[:2], color, w)
        if len(pts) >= 3:
            P.line(pts[1:3], color, w)
            v, a, c = pts[1], pts[0], pts[2]
            a1 = math.degrees(math.atan2(-(a[1] - v[1]), a[0] - v[0]))
            a2 = math.degrees(math.atan2(-(c[1] - v[1]), c[0] - v[0]))
            ext = ((a2 - a1 + 180) % 360) - 180
            r = min(24 * ui, 0.5 * min(math.hypot(a[0] - v[0], a[1] - v[1]),
                                       math.hypot(c[0] - v[0], c[1] - v[1])))
            if r > 2:
                P.arc(v[0], v[1], r, a1, ext, color, w)
        anchor = pts[1] if len(pts) >= 2 else pts[0]
    elif k == "circle":
        c, e = pts
        r = math.hypot(e[0] - c[0], e[1] - c[1])
        P.oval(c[0], c[1], r, color, w)
        P.line([c, e], color, 1)
        P.line([(c[0] - 4 * ui, c[1]), (c[0] + 4 * ui, c[1])], color, w)
        P.line([(c[0], c[1] - 4 * ui), (c[0], c[1] + 4 * ui)], color, w)
        anchor = (c[0], c[1] + r)
    elif k == "rect":
        a, b = pts
        P.rect(a[0], a[1], b[0], b[1], color, w)
        anchor = (max(a[0], b[0]), max(a[1], b[1]))
    if handles:
        for q in pts:
            h = 5 * ui
            P.rect(q[0] - h, q[1] - h, q[0] + h, q[1] + h, color, max(1, int(ui)), fill="#ffffff")
    if labels and anchor is not None:
        text = sh.info(ppm)[0]
        if text:
            P.text(anchor[0] + 8 * ui, anchor[1] + 8 * ui, text, color, 13 * ui)


def nice_length_mm(target_mm):
    if target_mm <= 0:
        return 1.0
    e = math.floor(math.log10(target_mm))
    for f in (1, 2, 5, 10):
        if f * 10 ** e >= target_mm * 0.7:
            return f * 10 ** e
    return 10 ** (e + 1)


def draw_scalebar(P, W, H, ppm_screen, ui):
    if not ppm_screen:
        return
    mm = nice_length_mm(W * 0.2 / ppm_screen)
    L = mm * ppm_screen
    x0, y = 20 * ui, H - 28 * ui
    w = max(2, int(round(3 * ui)))
    P.line([(x0, y), (x0 + L, y)], "#ffffff", w)
    for x in (x0, x0 + L):
        P.line([(x, y - 6 * ui), (x, y + 6 * ui)], "#ffffff", w)
    label = f"{mm * 1000:g} µm" if mm < 1 else f"{mm:g} mm"
    P.text(x0, y - 12 * ui, label, "#ffffff", 13 * ui, anchor="sw")


# ----------------------------------------------------------------------------- camera
MAX_TRY = [(3840, 2160), (2592, 1944), (2560, 1440), (2048, 1536), (1920, 1080),
           (1600, 1200), (1280, 960), (1280, 720), (800, 600), (640, 480)]


class Capture(threading.Thread):
    def __init__(self, index, size):
        super().__init__(daemon=True)
        self.index, self.size = index, size
        self.running = True
        self.lock = threading.Lock()
        self.frame, self.seq = None, 0
        self.error = None

    def run(self):
        cap = cv2.VideoCapture(self.index, CAM_BACKEND)
        if not cap.isOpened():
            self.error = f"Could not open camera {self.index}"
            return
        cap.set(cv2.CAP_PROP_FOURCC, cv2.VideoWriter_fourcc(*"MJPG"))
        if self.size == "max":
            for w, h in MAX_TRY:
                cap.set(cv2.CAP_PROP_FRAME_WIDTH, w)
                cap.set(cv2.CAP_PROP_FRAME_HEIGHT, h)
                ok, f = cap.read()
                if ok and f is not None and f.shape[1] == w and f.shape[0] == h:
                    break
        elif self.size:
            cap.set(cv2.CAP_PROP_FRAME_WIDTH, self.size[0])
            cap.set(cv2.CAP_PROP_FRAME_HEIGHT, self.size[1])
        fails = 0
        while self.running:
            ok, f = cap.read()
            if not ok or f is None:
                fails += 1
                if fails > 100:
                    self.error = "Camera stopped delivering frames"
                    break
                time.sleep(0.02)
                continue
            fails = 0
            with self.lock:
                self.frame, self.seq = f, self.seq + 1
        cap.release()

    def get(self, last_seq):
        with self.lock:
            if self.seq == last_seq or self.frame is None:
                return None, last_seq
            return self.frame, self.seq

    def stop(self):
        self.running = False
        self.join(timeout=3)


def render_view(img, s, ox, oy, W, H):
    out = np.full((H, W, 3), 32, np.uint8)
    ih, iw = img.shape[:2]
    x0, y0 = max(0, int(math.floor(-ox / s))), max(0, int(math.floor(-oy / s)))
    x1, y1 = min(iw, int(math.ceil((W - ox) / s))), min(ih, int(math.ceil((H - oy) / s)))
    if x1 <= x0 or y1 <= y0:
        return out
    dw, dh = int(round((x1 - x0) * s)), int(round((y1 - y0) * s))
    if dw < 1 or dh < 1:
        return out
    interp = cv2.INTER_AREA if s < 1 else (cv2.INTER_NEAREST if s >= 4 else cv2.INTER_LINEAR)
    res = cv2.resize(img[y0:y1, x0:x1], (dw, dh), interpolation=interp)
    dx, dy = int(round(ox + x0 * s)), int(round(oy + y0 * s))
    sx0, sy0 = max(0, -dx), max(0, -dy)
    ex, ey = min(dw, W - dx), min(dh, H - dy)
    if ex > sx0 and ey > sy0:
        out[dy + sy0:dy + ey, dx + sx0:dx + ex] = res[sy0:ey, sx0:ex]
    return out


# ----------------------------------------------------------------------------- viewer
class Viewer:
    HANDLE_TOL = 9
    HIT_TOL = 7

    def __init__(self, parent):
        self.cv = tk.Canvas(parent, bg="#202020", highlightthickness=0, cursor="arrow")
        self.img = None            # RGB uint8 array
        self.scale, self.ox, self.oy = 1.0, 0.0, 0.0
        self.fit = True
        self.shapes, self.sel = [], -1
        self.cal = None            # calibration Shape (kind 'calibrate')
        self.active = None
        self.tool = "select"
        self.ppm = None
        self.show_scalebar = True
        self.show_labels = True
        self.show_crosshair = False
        self.drag = None
        self.pan_last = None
        self.press_xy = None
        self.created_on_press = False
        self.dirty = True
        self._photo = None
        # callbacks
        self.on_shapes = lambda: None
        self.on_cal = lambda: None
        self.on_select = lambda i: None
        self.on_cursor = lambda s: None
        self.on_size = lambda w, h: None

        c = self.cv
        c.bind("<Configure>", self._configure)
        c.bind("<ButtonPress-1>", self._press)
        c.bind("<B1-Motion>", self._drag)
        c.bind("<ButtonRelease-1>", self._release)
        c.bind("<Double-Button-1>", self._double)
        c.bind("<ButtonPress-3>", self._right)
        c.bind("<ButtonPress-2>", self._pan_start)
        c.bind("<B2-Motion>", self._pan_move)
        c.bind("<Motion>", self._motion)
        c.bind("<MouseWheel>", self._wheel)
        c.bind("<Button-4>", lambda e: self.set_zoom(self.scale * 1.25, e.x, e.y))  # Linux
        c.bind("<Button-5>", lambda e: self.set_zoom(self.scale / 1.25, e.x, e.y))

    # -- coordinates
    def to_img(self, x, y):
        return ((x - self.ox) / self.scale, (y - self.oy) / self.scale)

    def to_scr(self, p):
        return (p[0] * self.scale + self.ox, p[1] * self.scale + self.oy)

    # -- image / view
    def set_image(self, rgb):
        changed = self.img is None or self.img.shape[:2] != rgb.shape[:2]
        self.img = rgb
        if changed:
            self.shapes, self.sel, self.active, self.cal, self.drag = [], -1, None, None, None
            self.fit_view()
            self.on_size(rgb.shape[1], rgb.shape[0])
            self.on_shapes()
            self.on_cal()
        self.dirty = True

    def fit_view(self):
        self.fit = True
        if self.img is None:
            return
        W, H = max(self.cv.winfo_width(), 2), max(self.cv.winfo_height(), 2)
        ih, iw = self.img.shape[:2]
        self.scale = min(W / iw, H / ih)
        self.ox, self.oy = (W - iw * self.scale) / 2, (H - ih * self.scale) / 2
        self.dirty = True

    def set_zoom(self, s, ax=None, ay=None):
        if self.img is None:
            return
        W, H = self.cv.winfo_width(), self.cv.winfo_height()
        ax = W / 2 if ax is None else ax
        ay = H / 2 if ay is None else ay
        s = max(0.02, min(64.0, s))
        self.ox = ax - (ax - self.ox) * s / self.scale
        self.oy = ay - (ay - self.oy) * s / self.scale
        self.scale, self.fit = s, False
        self.dirty = True

    def _configure(self, _e):
        if self.fit:
            self.fit_view()
        self.dirty = True

    def _wheel(self, e):
        steps = e.delta / 120.0 if abs(e.delta) >= 120 else (1 if e.delta > 0 else -1)  # macOS sends small deltas
        self.set_zoom(self.scale * 1.25 ** steps, e.x, e.y)

    def _pan_start(self, e):
        self.pan_last = (e.x, e.y)

    def _pan_move(self, e):
        if self.pan_last:
            self.ox += e.x - self.pan_last[0]
            self.oy += e.y - self.pan_last[1]
            self.pan_last = (e.x, e.y)
            self.fit = False
            self.dirty = True

    # -- tools
    def set_tool(self, tool):
        self.tool = tool
        self.active = None
        self.cv.config(cursor="arrow" if tool == "select" else "crosshair")
        self.dirty = True

    def select(self, i):
        if i != self.sel:
            self.sel = i
            self.on_select(i)
        self.dirty = True

    def delete_selected(self):
        if 0 <= self.sel < len(self.shapes):
            del self.shapes[self.sel]
            self.sel = -1
            self.on_select(-1)
            self.on_shapes()
            self.dirty = True

    def clear_all(self):
        self.shapes, self.sel, self.active = [], -1, None
        self.on_select(-1)
        self.on_shapes()
        self.dirty = True

    def clear_cal(self):
        self.cal = None
        self.on_cal()
        self.dirty = True

    def cancel(self):
        self.active = None
        self.dirty = True

    def finish_polyline(self):
        a = self.active
        if a is not None and a.kind == "polyline":
            a.pts.pop()
            self._commit(a)

    # -- hit testing (screen space)
    def _hit_handle(self, sh, x, y):
        for i, p in enumerate(sh.pts):
            if dist(self.to_scr(p), (x, y)) <= self.HANDLE_TOL:
                return i
        return None

    def _hit_shape(self, sh, x, y):
        pts = [self.to_scr(p) for p in sh.pts]
        t, q = self.HIT_TOL, (x, y)
        k = sh.kind
        if k in ("line", "polyline"):
            return any(seg_dist(q, pts[i], pts[i + 1]) <= t for i in range(len(pts) - 1))
        if k == "angle":
            return seg_dist(q, pts[0], pts[1]) <= t or seg_dist(q, pts[1], pts[2]) <= t
        if k == "circle":
            return abs(dist(q, pts[0]) - dist(pts[0], pts[1])) <= t or dist(q, pts[0]) <= t
        if k == "rect":
            (x0, y0), (x1, y1) = pts
            c = [(x0, y0), (x1, y0), (x1, y1), (x0, y1)]
            return any(seg_dist(q, c[i], c[(i + 1) % 4]) <= t for i in range(4))
        return False

    # -- mouse
    def _press(self, e):
        self.cv.focus_set()
        if self.img is None:
            return
        p = self.to_img(e.x, e.y)
        self.press_xy = (e.x, e.y)
        self.created_on_press = False
        if self.active is not None:
            self._add_point(p)
            return
        if self.tool == "select":
            if 0 <= self.sel < len(self.shapes):
                idx = self._hit_handle(self.shapes[self.sel], e.x, e.y)
                if idx is not None:
                    self.drag = ("handle", self.shapes[self.sel], idx, False)
                    return
            for i in reversed(range(len(self.shapes))):
                if self._hit_shape(self.shapes[i], e.x, e.y):
                    self.select(i)
                    self.drag = ("move", self.shapes[i], p, False)
                    return
            self.select(-1)
            self.pan_last = (e.x, e.y)
            return
        if self.tool == "calibrate" and self.cal is not None:
            idx = self._hit_handle(self.cal, e.x, e.y)
            if idx is not None:
                self.drag = ("handle", self.cal, idx, True)
                return
        self.active = Shape(self.tool, [p, p])
        self.created_on_press = True
        self.dirty = True

    def _add_point(self, p):
        a = self.active
        a.pts[-1] = list(p)
        need = NEED[a.kind]
        if need and len(a.pts) >= need:
            self._commit(a)
        else:
            a.pts.append(list(p))
        self.dirty = True

    def _commit(self, sh):
        self.active = None
        if sh.degenerate():
            self.dirty = True
            return
        if sh.kind == "calibrate":
            self.cal = sh
            self.on_cal()
        else:
            self.shapes.append(sh)
            self.on_shapes()
        self.dirty = True

    def _double(self, e):
        if self.active is not None and self.active.kind == "polyline":
            self.finish_polyline()
        else:
            self._press(e)

    def _right(self, _e):
        a = self.active
        if a is None:
            return
        if a.kind == "polyline" and len(a.pts) > 2:
            self.finish_polyline()
        else:
            self.cancel()

    def _drag(self, e):
        if self.img is None:
            return
        p = self.to_img(e.x, e.y)
        if self.drag:
            kind, sh, ref, is_cal = self.drag
            if kind == "handle":
                sh.pts[ref] = list(p)
            else:
                dx, dy = p[0] - ref[0], p[1] - ref[1]
                for q in sh.pts:
                    q[0] += dx
                    q[1] += dy
                self.drag = (kind, sh, p, is_cal)
            self.dirty = True
            if is_cal:
                self.on_cal()
            return
        if self.pan_last:
            self.ox += e.x - self.pan_last[0]
            self.oy += e.y - self.pan_last[1]
            self.pan_last = (e.x, e.y)
            self.fit = False
            self.dirty = True
            return
        self._motion(e)

    def _release(self, e):
        if self.drag:
            if not self.drag[3]:
                self.on_shapes()
            self.drag = None
        self.pan_last = None
        a = self.active
        if a is not None and self.created_on_press and NEED[a.kind] == 2 and self.press_xy:
            if dist(self.press_xy, (e.x, e.y)) > 6:
                self._add_point(self.to_img(e.x, e.y))
        self.created_on_press = False

    def _motion(self, e):
        if self.img is None:
            return
        p = self.to_img(e.x, e.y)
        if self.active is not None:
            self.active.pts[-1] = list(p)
            self.dirty = True
        ih, iw = self.img.shape[:2]
        inside = 0 <= p[0] < iw and 0 <= p[1] < ih
        txt = f"x {p[0]:.1f}  y {p[1]:.1f} px" if inside else "outside image"
        self.on_cursor(f"{txt}    zoom {self.scale * 100:.0f}%")

    # -- drawing
    def redraw(self):
        self.dirty = False
        c = self.cv
        W, H = c.winfo_width(), c.winfo_height()
        if W < 2 or H < 2:
            return
        c.delete("all")
        if self.img is None:
            c.create_text(W / 2, H / 2, fill="#888888", font=("Segoe UI", 14),
                          text="No image - start a camera or open an image file")
            return
        arr = render_view(self.img, self.scale, self.ox, self.oy, W, H)
        self._photo = ImageTk.PhotoImage(Image.fromarray(arr))
        c.create_image(0, 0, image=self._photo, anchor="nw")
        P = CanvasPainter(c)
        for i, sh in enumerate(self.shapes):
            selected = i == self.sel
            draw_shape(P, sh, self.to_scr, 1, self.ppm, COL_SEL if selected else COL_SHAPE,
                       self.show_labels, selected and self.tool == "select")
        if self.cal is not None:
            draw_shape(P, self.cal, self.to_scr, 1, None, COL_CAL, True, self.tool == "calibrate")
        if self.active is not None:
            col = COL_CAL if self.active.kind == "calibrate" else COL_ACTIVE
            draw_shape(P, self.active, self.to_scr, 1, self.ppm, col, True)
        if self.show_crosshair:
            cx, cy = self.to_scr((self.img.shape[1] / 2, self.img.shape[0] / 2))
            for pts in ([(0, cy), (W, cy)], [(cx, 0), (cx, H)]):
                c.create_line(*pts[0], *pts[1], fill="#000000", width=3, tags="ov")
                c.create_line(*pts[0], *pts[1], fill="#ff1744", width=1, tags="ov")
            c.create_oval(cx - 12, cy - 12, cx + 12, cy + 12, outline="#ff1744", tags="ov")
        if self.show_scalebar and self.ppm:
            draw_scalebar(P, W, H, self.ppm * self.scale, 1)

    def annotated_image(self):
        """Full-resolution image with measurements burned in."""
        pil = Image.fromarray(self.img).convert("RGB")
        H, W = self.img.shape[:2]
        ui = max(1.0, max(W, H) / 1000.0)
        P = PILPainter(pil, ui)
        ident = lambda p: (p[0], p[1])
        for sh in self.shapes:
            draw_shape(P, sh, ident, ui, self.ppm, COL_SHAPE, True)
        if self.show_scalebar and self.ppm:
            draw_scalebar(P, W, H, self.ppm, ui)
        return pil


# ----------------------------------------------------------------------------- app
class App:
    def __init__(self, root):
        self.root = root
        root.title("Microscope Viewer")
        root.geometry("1500x940")
        root.minsize(1100, 760)
        self.capture = None
        self.last_seq = 0
        self.cams = []
        self.profiles, self.active_profile = {}, None
        self._load_cal()
        self._build()
        self._update_ppm()
        root.protocol("WM_DELETE_WINDOW", self._close)
        root.after(15, self._tick)
        root.after(300, self._startup)

    # ---- UI construction
    def _build(self):
        root = self.root
        style = ttk.Style()
        if "vista" in style.theme_names():
            style.theme_use("vista")
        style.configure("Tool.TRadiobutton", padding=4)

        main = ttk.Frame(root)
        main.pack(fill="both", expand=True)
        side = ttk.Frame(main)
        side.pack(side="right", fill="y", padx=(0, 6), pady=6)
        self.viewer = Viewer(main)
        self.viewer.cv.pack(side="left", fill="both", expand=True, padx=6, pady=6)
        v = self.viewer
        v.on_shapes = self._refresh_table
        v.on_cal = self._update_cal_readout
        v.on_select = self._table_select
        v.on_cursor = lambda s: self.cursor_var.set(s)
        v.on_size = self._on_image_size

        self.status = tk.StringVar(value="Ready")
        self.cursor_var = tk.StringVar()
        bar = ttk.Frame(root)
        bar.pack(side="bottom", fill="x")
        ttk.Label(bar, textvariable=self.status, relief="sunken", anchor="w").pack(side="left", fill="x", expand=True)
        ttk.Label(bar, textvariable=self.cursor_var, relief="sunken", width=42, anchor="e").pack(side="right")

        # camera
        g = ttk.LabelFrame(side, text="Camera")
        g.pack(fill="x", pady=(0, 6))
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=3)
        self.cam_combo = ttk.Combobox(r, state="readonly", width=18)
        self.cam_combo.pack(side="left", fill="x", expand=True)
        ttk.Button(r, text="Refresh", width=8, command=self.refresh_cameras).pack(side="left", padx=(4, 0))
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=3)
        ttk.Label(r, text="Resolution").pack(side="left")
        self.res_combo = ttk.Combobox(r, state="readonly", values=RESOLUTIONS, width=16)
        self.res_combo.current(0)
        self.res_combo.pack(side="left", padx=4, fill="x", expand=True)
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=3)
        self.start_btn = ttk.Button(r, text="Start", command=self.toggle_camera)
        self.start_btn.pack(side="left", fill="x", expand=True)
        self.freeze_var = tk.BooleanVar(value=False)
        ttk.Checkbutton(r, text="Freeze", variable=self.freeze_var).pack(side="left", padx=6)
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=(3, 6))
        ttk.Button(r, text="Open image...", command=self.open_image).pack(side="left", fill="x", expand=True)
        ttk.Button(r, text="Save raw", command=lambda: self.save_image(False)).pack(side="left", padx=3)
        ttk.Button(r, text="Save + overlay", command=lambda: self.save_image(True)).pack(side="left")

        # tools
        g = ttk.LabelFrame(side, text="Measure")
        g.pack(fill="x", pady=(0, 6))
        self.tool_var = tk.StringVar(value="select")
        tools = [("select", "Select/Move (V)"), ("line", "Distance (L)"), ("polyline", "Path (P)"),
                 ("angle", "Angle (A)"), ("circle", "Circle (C)"), ("rect", "Rectangle (R)")]
        self.hotkeys = {"v": "select", "l": "line", "p": "polyline", "a": "angle", "c": "circle",
                        "r": "rect", "k": "calibrate"}
        grid = ttk.Frame(g)
        grid.pack(fill="x", padx=6, pady=4)
        for i, (k, label) in enumerate(tools):
            ttk.Radiobutton(grid, text=label, value=k, variable=self.tool_var, style="Tool.TRadiobutton",
                            command=self._tool_changed).grid(row=i // 2, column=i % 2, sticky="w", padx=2)
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=(0, 4))
        self.scalebar_var = tk.BooleanVar(value=True)
        self.labels_var = tk.BooleanVar(value=True)
        ttk.Checkbutton(r, text="Scale bar", variable=self.scalebar_var, command=self._view_opts).pack(side="left")
        ttk.Checkbutton(r, text="Labels", variable=self.labels_var, command=self._view_opts).pack(side="left", padx=8)
        self.cross_var = tk.BooleanVar(value=False)
        ttk.Checkbutton(r, text="Crosshair", variable=self.cross_var, command=self._view_opts).pack(side="left")
        ttk.Button(r, text="Fit", width=4, command=self.viewer.fit_view).pack(side="right")
        ttk.Button(r, text="1:1", width=4, command=lambda: self.viewer.set_zoom(1.0)).pack(side="right", padx=3)
        ttk.Label(g, text="Click-click or drag to draw.  Wheel = zoom, middle-drag = pan.\n"
                          "Path: double-click to finish. Esc cancels.",
                  foreground="#555555", justify="left").pack(anchor="w", padx=6, pady=(0, 4))

        # calibration
        g = ttk.LabelFrame(side, text="Calibration")
        g.pack(fill="x", pady=(0, 6))
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=3)
        ttk.Label(r, text="Active").pack(side="left")
        self.prof_combo = ttk.Combobox(r, state="readonly", width=18)
        self.prof_combo.pack(side="left", padx=4, fill="x", expand=True)
        self.prof_combo.bind("<<ComboboxSelected>>", self._profile_selected)
        ttk.Button(r, text="Delete", width=6, command=self._delete_profile).pack(side="left")
        self.cal_status = tk.StringVar()
        self.cal_status_lbl = ttk.Label(g, textvariable=self.cal_status, wraplength=360, justify="left")
        self.cal_status_lbl.pack(anchor="w", padx=6)
        ttk.Separator(g).pack(fill="x", padx=6, pady=4)
        ttk.Label(g, text="New: ruler under microscope, in focus: mark two\n"
                          "ruler lines a known distance apart.", justify="left",
                  foreground="#555555").pack(anchor="w", padx=6)
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=3)
        ttk.Radiobutton(r, text="1. Draw calibration line (K)", value="calibrate", variable=self.tool_var,
                        style="Tool.TRadiobutton", command=self._tool_changed).pack(side="left")
        ttk.Button(r, text="Clear", width=6, command=self.viewer.clear_cal).pack(side="right")
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=3)
        ttk.Label(r, text="2. Known distance").pack(side="left")
        self.known_var = tk.StringVar(value="10.000")
        sp = ttk.Spinbox(r, from_=0.001, to=1000, increment=0.5, width=9, textvariable=self.known_var,
                         command=self._update_cal_readout)
        sp.pack(side="left", padx=4)
        sp.bind("<KeyRelease>", lambda e: self._update_cal_readout())
        ttk.Label(r, text="mm").pack(side="left")
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=3)
        ttk.Label(r, text="3. Name").pack(side="left")
        self.name_var = tk.StringVar(value="10x objective")
        ttk.Entry(r, textvariable=self.name_var).pack(side="left", padx=4, fill="x", expand=True)
        self.cal_readout = tk.StringVar(value="Measured: -")
        ttk.Label(g, textvariable=self.cal_readout, font=("Segoe UI", 9, "bold")).pack(anchor="w", padx=6)
        ttk.Button(g, text="4. Apply & save calibration", command=self.apply_calibration).pack(
            fill="x", padx=6, pady=(4, 6))

        # measurements
        g = ttk.LabelFrame(side, text="Measurements")
        g.pack(fill="both", expand=True)
        self.tree = ttk.Treeview(g, columns=("type", "value"), show="headings", height=4, selectmode="browse")
        self.tree.heading("type", text="Type")
        self.tree.heading("value", text="Value")
        self.tree.column("type", width=80, stretch=False)
        self.tree.column("value", width=220)
        self.tree.pack(fill="both", expand=True, padx=6, pady=(4, 2))
        self.tree.bind("<<TreeviewSelect>>", self._tree_selected)
        r = ttk.Frame(g)
        r.pack(fill="x", padx=6, pady=(0, 6))
        ttk.Button(r, text="Delete", command=self.viewer.delete_selected).pack(side="left")
        ttk.Button(r, text="Clear all", command=self.viewer.clear_all).pack(side="left", padx=4)
        ttk.Button(r, text="Export CSV...", command=self.export_csv).pack(side="right")

        root.bind("<Escape>", lambda e: self.viewer.cancel())
        root.bind("<Return>", lambda e: self.viewer.finish_polyline())
        root.bind("<Delete>", self._key_delete)
        root.bind("<BackSpace>", self._key_delete)
        root.bind("<Key>", self._key)
        self._refresh_profile_combo()

    # ---- keyboard
    def _typing(self):
        return isinstance(self.root.focus_get(), (ttk.Entry, tk.Entry, ttk.Spinbox, ttk.Combobox))

    def _key(self, e):
        if self._typing() or e.state & 0x4:
            return
        t = self.hotkeys.get(e.keysym.lower())
        if t:
            self.tool_var.set(t)
            self._tool_changed()

    def _key_delete(self, _e):
        if not self._typing():
            self.viewer.delete_selected()

    def _tool_changed(self):
        self.viewer.set_tool(self.tool_var.get())

    def _view_opts(self):
        self.viewer.show_scalebar = self.scalebar_var.get()
        self.viewer.show_labels = self.labels_var.get()
        self.viewer.show_crosshair = self.cross_var.get()
        self.viewer.dirty = True

    # ---- camera
    def _startup(self):
        self.refresh_cameras()
        if self.cams:
            self.toggle_camera()

    def refresh_cameras(self):
        self.root.config(cursor="watch")
        self.root.update_idletasks()
        running = self.capture.index if self.capture else None
        found = []
        for i in range(8):
            if i == running:
                found.append(i)
                continue
            cap = cv2.VideoCapture(i, CAM_BACKEND)
            if cap.isOpened():
                found.append(i)
            cap.release()
        self.cams = found
        self.cam_combo["values"] = [f"Camera {i}" for i in found]
        if found:
            self.cam_combo.current(found.index(running) if running in found else 0)
        else:
            self.cam_combo.set("")
        self.root.config(cursor="")
        self.status.set(f"{len(found)} camera(s) found" if found else "No cameras found")

    def toggle_camera(self):
        if self.capture:
            self._stop_camera()
            self.status.set("Camera stopped")
            return
        sel = self.cam_combo.current()
        if sel < 0 or sel >= len(self.cams):
            messagebox.showinfo("Camera", "No camera selected. Plug in the UVC camera and press Refresh.")
            return
        res = self.res_combo.get()
        size = "max" if res == RESOLUTIONS[0] else None if res == RESOLUTIONS[1] else tuple(int(x) for x in res.split("x"))
        self.capture = Capture(self.cams[sel], size)
        self.last_seq = 0
        self.capture.start()
        self.start_btn.config(text="Stop")
        self.status.set(f"Starting camera {self.cams[sel]}...")

    def _stop_camera(self):
        if self.capture:
            self.capture.stop()
            self.capture = None
        self.start_btn.config(text="Start")

    def _tick(self):
        try:
            cap = self.capture
            if cap is not None:
                if cap.error:
                    err = cap.error
                    self._stop_camera()
                    self.status.set(err)
                elif not self.freeze_var.get():
                    frame, self.last_seq = cap.get(self.last_seq)
                    if frame is not None:
                        self.viewer.set_image(cv2.cvtColor(frame, cv2.COLOR_BGR2RGB))
                        h, w = frame.shape[:2]
                        self.status.set(f"Camera {cap.index} live - {w}x{h}")
            if self.viewer.dirty:
                self.viewer.redraw()
        finally:
            self.root.after(15, self._tick)

    def _on_image_size(self, w, h):
        self._update_ppm()

    # ---- image files
    def open_image(self):
        path = filedialog.askopenfilename(title="Open image",
                                          filetypes=[("Images", "*.png *.jpg *.jpeg *.bmp *.tif *.tiff"), ("All", "*.*")])
        if not path:
            return
        try:
            arr = np.array(Image.open(path).convert("RGB"))
        except Exception as ex:  # noqa: BLE001
            messagebox.showerror("Open image", str(ex))
            return
        self.freeze_var.set(True)
        self.viewer.set_image(arr)
        self.status.set(f"{os.path.basename(path)} - {arr.shape[1]}x{arr.shape[0]} (frozen; uncheck Freeze for live)")

    def save_image(self, annotated):
        if self.viewer.img is None:
            return
        name = datetime.now().strftime("microscope_%Y%m%d_%H%M%S") + ("_annotated" if annotated else "")
        path = filedialog.asksaveasfilename(defaultextension=".png", initialfile=name,
                                            filetypes=[("PNG", "*.png"), ("JPEG", "*.jpg"), ("TIFF", "*.tif")])
        if not path:
            return
        img = self.viewer.annotated_image() if annotated else Image.fromarray(self.viewer.img)
        img.save(path)
        self.status.set(f"Saved {path}")

    def export_csv(self):
        if not self.viewer.shapes:
            return
        path = filedialog.asksaveasfilename(defaultextension=".csv", initialfile="measurements.csv",
                                            filetypes=[("CSV", "*.csv")])
        if not path:
            return
        ppm = self.viewer.ppm
        with open(path, "w", newline="", encoding="utf-8-sig") as f:
            w = csv.writer(f)
            w.writerow(["#", "type", "value", "details", "pixels_per_mm", "points_px"])
            for i, sh in enumerate(self.viewer.shapes, 1):
                lab, det = sh.info(ppm)
                w.writerow([i, TYPE_NAME[sh.kind], lab, det, f"{ppm:.4f}" if ppm else "uncalibrated",
                            ";".join(f"({p[0]:.2f},{p[1]:.2f})" for p in sh.pts)])
        self.status.set(f"Exported {path}")

    # ---- measurement table
    def _refresh_table(self):
        self.tree.delete(*self.tree.get_children())
        for i, sh in enumerate(self.viewer.shapes):
            lab, det = sh.info(self.viewer.ppm)
            self.tree.insert("", "end", iid=str(i), values=(f"{i + 1} {TYPE_NAME[sh.kind]}",
                                                            lab + (f"   ({det})" if det else "")))
        if 0 <= self.viewer.sel < len(self.viewer.shapes):
            self.tree.selection_set(str(self.viewer.sel))

    def _table_select(self, i):
        self.tree.unbind("<<TreeviewSelect>>")
        if i >= 0 and str(i) in self.tree.get_children():
            self.tree.selection_set(str(i))
        else:
            self.tree.selection_remove(*self.tree.selection())
        self.root.after(1, lambda: self.tree.bind("<<TreeviewSelect>>", self._tree_selected))

    def _tree_selected(self, _e):
        s = self.tree.selection()
        if s:
            self.viewer.select(int(s[0]))

    # ---- calibration
    def _load_cal(self):
        try:
            with open(CAL_FILE, "r", encoding="utf-8") as f:
                d = json.load(f)
            self.profiles = d.get("profiles", {})
            self.active_profile = d.get("active")
            if self.active_profile not in self.profiles:
                self.active_profile = None
        except (OSError, ValueError):
            self.profiles, self.active_profile = {}, None

    def _save_cal(self):
        try:
            with open(CAL_FILE, "w", encoding="utf-8") as f:
                json.dump({"active": self.active_profile, "profiles": self.profiles}, f, indent=2)
        except OSError as ex:
            messagebox.showwarning("Calibration", f"Could not save calibration file:\n{ex}")

    def _refresh_profile_combo(self):
        names = list(self.profiles)
        self.prof_combo["values"] = ["(none)"] + names
        self.prof_combo.set(self.active_profile or "(none)")

    def _profile_selected(self, _e):
        v = self.prof_combo.get()
        self.active_profile = v if v in self.profiles else None
        self._save_cal()
        self._update_ppm()

    def _delete_profile(self):
        n = self.active_profile
        if n and messagebox.askyesno("Calibration", f"Delete calibration '{n}'?"):
            del self.profiles[n]
            self.active_profile = None
            self._save_cal()
            self._refresh_profile_combo()
            self._update_ppm()

    def _update_ppm(self):
        """Effective pixels/mm for the current image size, from the active profile."""
        v = self.viewer
        prof = self.profiles.get(self.active_profile) if self.active_profile else None
        warn = ""
        if prof is None:
            v.ppm = None
            txt = "Not calibrated - values are in pixels."
        else:
            iw = v.img.shape[1] if v.img is not None else prof["w"]
            ih = v.img.shape[0] if v.img is not None else prof["h"]
            v.ppm = prof["ppm"] * iw / prof["w"]
            txt = (f"'{self.active_profile}': {v.ppm:.2f} px/mm  ({1000 / v.ppm:.2f} µm/px)\n"
                   f"calibrated at {prof['w']}x{prof['h']}, current {iw}x{ih}")
            if abs((iw / ih) - (prof["w"] / prof["h"])) > 0.02:
                warn = "\nAspect ratio differs from calibration - recalibrate at this resolution!"
        self.cal_status.set(txt + warn)
        self.cal_status_lbl.config(foreground="#c62828" if (warn or prof is None) else "#1b5e20")
        self._refresh_table()
        v.dirty = True

    def _known_mm(self):
        try:
            return float(self.known_var.get().replace(",", "."))
        except ValueError:
            return None

    def _update_cal_readout(self):
        c = self.viewer.cal
        if c is None:
            self.cal_readout.set("Measured: - (draw a calibration line)")
            return
        L = dist(c.pts[0], c.pts[1])
        k = self._known_mm()
        if k and k > 0 and L > 0:
            self.cal_readout.set(f"Line {L:.2f} px  =>  {L / k:.2f} px/mm  ({1000 * k / L:.2f} µm/px)")
        else:
            self.cal_readout.set(f"Line {L:.2f} px")

    def apply_calibration(self):
        v = self.viewer
        k = self._known_mm()
        if v.img is None:
            messagebox.showinfo("Calibration", "No image available.")
            return
        if v.cal is None:
            messagebox.showinfo("Calibration", "Draw a calibration line across the ruler first (tool K).")
            return
        if not k or k <= 0:
            messagebox.showinfo("Calibration", "Enter a valid known distance in mm.")
            return
        name = self.name_var.get().strip() or "Calibration"
        if name in self.profiles and not messagebox.askyesno("Calibration", f"Overwrite calibration '{name}'?"):
            return
        L = dist(v.cal.pts[0], v.cal.pts[1])
        h, w = v.img.shape[:2]
        self.profiles[name] = {"ppm": L / k, "w": w, "h": h, "known_mm": k, "line_px": L,
                               "date": datetime.now().isoformat(timespec="seconds")}
        self.active_profile = name
        self._save_cal()
        self._refresh_profile_combo()
        v.clear_cal()
        self._update_ppm()
        self.tool_var.set("line")
        self._tool_changed()
        self.status.set(f"Calibration '{name}' saved: {L / k:.2f} px/mm. Verify by measuring a ruler span.")

    def _close(self):
        self._stop_camera()
        self.root.destroy()


def main():
    try:  # crisp UI and 1:1 pixel mapping on scaled Windows displays
        import ctypes
        ctypes.windll.shcore.SetProcessDpiAwareness(1)
    except Exception:  # noqa: BLE001
        pass
    root = tk.Tk()
    App(root)
    root.mainloop()


if __name__ == "__main__":
    main()

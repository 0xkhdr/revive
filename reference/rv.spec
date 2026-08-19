# -*- mode: python ; coding: utf-8 -*-
"""PyInstaller spec for the Revive (rv) CLI binary.

Usage:
    pyinstaller rv.spec --clean
    dist/rv --help
"""

import os
from PyInstaller.utils.hooks import collect_data_files

block_cipher = None

# Collect built-in plugin YAML manifests and GUI static assets bundled with the package
datas = collect_data_files("rv", includes=["plugins/builtin/*/plugin.yaml", "gui/static/*"])

a = Analysis(
    ["src/rv/__main__.py"],
    pathex=[os.path.abspath("src")],
    binaries=[],
    datas=datas,
    hiddenimports=[
        # Typer / Click introspection can miss these at bundle time
        "typer",
        "typer.main",
        "rich",
        "rich.console",
        "rich.panel",
        "rich.table",
        "rich.syntax",
        "rich.progress",
        # Pydantic v2 core uses Rust extensions — enumerate the common sub-modules
        "pydantic",
        "pydantic.v1",
        "pydantic_core",
        # PyYAML
        "yaml",
        "_yaml",
        # Jinja2
        "jinja2",
        "jinja2.ext",
        # Watchdog backend selection (inotify on Linux, kqueue on macOS)
        "watchdog.observers",
        "watchdog.observers.inotify",
        "watchdog.observers.fsevents",
        # pyrage (Rust extension — the .so is auto-collected, but declare module explicitly)
        "pyrage",
        "pyrage.x25519",
        # rv internal modules that may be imported dynamically
        "rv.cli.main",
        "rv.services.restore",
        "rv.services.backup",
        "rv.services.doctor",
        "rv.services.status",
        "rv.services.recovery",
        "rv.services.workspace",
        "rv.services.handlers",
        "rv.plugins.loader",
        "rv.plugins.sandbox",
        "rv.plugins.sandbox_wrapper",
        "rv.providers.apt",
        "rv.providers.brew",
        "rv.providers.cargo",
        "rv.providers.dnf",
        "rv.providers.docker",
        "rv.providers.flatpak",
        "rv.providers.nix",
        "rv.providers.node",
        "rv.providers.pacman",
        "rv.providers.pip",
        "rv.providers.snap",
        "rv.security.encryptor",
        "rv.security.scrubber",
        "rv.security.zerobuffer",
        "rv.transactions.context",
        "rv.transactions.atomic",
        "rv.transactions.lock",
        "rv.utils.interpolate",
        "rv.utils.path",
        "rv.utils.platform",
    ],
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=[
        # Exclude test dependencies not needed at runtime
        "pytest",
        "pytest_cov",
        "mypy",
        "ruff",
        "bandit",
        "pyinstaller",
    ],
    win_no_prefer_redirects=False,
    win_private_assemblies=False,
    cipher=block_cipher,
    noarchive=False,
)

pyz = PYZ(a.pure, a.zipped_data, cipher=block_cipher)

exe = EXE(
    pyz,
    a.scripts,
    a.binaries,
    a.zipfiles,
    a.datas,
    [],
    name="rv",
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=True,
    upx_exclude=[],
    runtime_tmpdir=None,
    console=True,
    # Ensure the bundled binary can locate its data files via sys._MEIPASS
    disable_windowed_traceback=False,
    argv_emulation=False,
    target_arch=None,
    codesign_identity=None,
    entitlements_file=None,
)

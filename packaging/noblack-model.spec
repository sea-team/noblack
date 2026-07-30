# -*- mode: python ; coding: utf-8 -*-

from pathlib import Path

from PyInstaller.utils.hooks import collect_all, collect_submodules


project_root = Path(SPEC).resolve().parent.parent
model_source = project_root / "model_service" / "src"

datas = []
binaries = []
hiddenimports = []
for package_name in ("transformers", "tokenizers", "safetensors", "pypinyin"):
    package_datas, package_binaries, package_hiddenimports = collect_all(package_name)
    datas += package_datas
    binaries += package_binaries
    hiddenimports += package_hiddenimports

hiddenimports += collect_submodules("noblack_model")
hiddenimports += collect_submodules("noblack_data")
hiddenimports += collect_submodules("backports")

analysis = Analysis(
    [str(project_root / "model_service" / "app.py")],
    pathex=[str(model_source)],
    binaries=binaries,
    datas=datas,
    hiddenimports=sorted(set(hiddenimports)),
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=("transformers.testing_utils",),
    noarchive=False,
    optimize=0,
)
pyz = PYZ(analysis.pure)

executable = EXE(
    pyz,
    analysis.scripts,
    analysis.binaries,
    analysis.datas,
    [],
    name="noblack-model",
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=False,
    console=True,
    disable_windowed_traceback=False,
    argv_emulation=False,
    target_arch=None,
    codesign_identity=None,
    entitlements_file=None,
)

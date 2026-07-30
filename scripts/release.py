from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tarfile
import zipfile
from pathlib import Path
from typing import Any


HASH_CHUNK_SIZE = 8 * 1024 * 1024
LFS_POINTER_PREFIX = b"version https://git-lfs.github.com/spec/v1"
ROOT = Path(__file__).resolve().parent.parent
PRODUCTION_MODELS = ("lite-production-v1", "macbert-production-v1")
TARGETS = {
    "linux-amd64": {
        "go_binary": "noblack",
        "model_binary": "noblack-model",
        "launcher_directory": "linux",
    },
    "windows-amd64": {
        "go_binary": "noblack.exe",
        "model_binary": "noblack-model.exe",
        "launcher_directory": "windows",
    },
}


def validate_model_file(
    root: Path,
    relative_path: str,
    expected_size: int,
    expected_sha256: str,
) -> int:
    model_path = root / relative_path
    with model_path.open("rb") as model_stream:
        first_bytes = model_stream.read(128)
        if first_bytes.startswith(LFS_POINTER_PREFIX):
            raise ValueError(f"{relative_path} is a Git LFS pointer")
        digest = hashlib.sha256()
        digest.update(first_bytes)
        while chunk := model_stream.read(HASH_CHUNK_SIZE):
            digest.update(chunk)

    actual_size = model_path.stat().st_size
    if actual_size != expected_size:
        raise ValueError(
            f"{relative_path} size mismatch: expected {expected_size}, got {actual_size}"
        )
    actual_sha256 = digest.hexdigest()
    if actual_sha256 != expected_sha256:
        raise ValueError(
            f"{relative_path} SHA-256 mismatch: expected {expected_sha256}, "
            f"got {actual_sha256}"
        )
    return actual_size


def validate_release_inputs(root: Path) -> dict[str, int]:
    checksums_path = root / "packaging" / "model-checksums.json"
    checksums: dict[str, dict[str, Any]] = json.loads(
        checksums_path.read_text(encoding="utf-8")
    )
    model_bytes = 0
    for relative_path, expected in sorted(checksums.items()):
        model_bytes += validate_model_file(
            root,
            relative_path,
            int(expected["size"]),
            str(expected["sha256"]),
        )
        model_config = (root / relative_path).with_name("model_config.json")
        if not model_config.is_file():
            raise FileNotFoundError(f"missing model config: {model_config}")

    words_path = root / "words.json"
    if not words_path.is_file():
        raise FileNotFoundError(f"missing word database: {words_path}")
    duplicate_words_path = root / "data" / "words.json"
    if duplicate_words_path.is_file():
        raise ValueError(
            f"duplicate word database: remove {duplicate_words_path}; "
            f"use {words_path} as the repository source"
        )
    return {
        "model_count": len(checksums),
        "model_bytes": model_bytes,
        "words_size": words_path.stat().st_size,
    }


def write_manifest(package_directory: Path) -> Path:
    manifest_path = package_directory / "SHA256SUMS"
    lines: list[str] = []
    for file_path in sorted(path for path in package_directory.rglob("*") if path.is_file()):
        if file_path == manifest_path:
            continue
        digest = hashlib.sha256()
        with file_path.open("rb") as file_stream:
            while chunk := file_stream.read(HASH_CHUNK_SIZE):
                digest.update(chunk)
        relative_path = file_path.relative_to(package_directory).as_posix()
        lines.append(f"{digest.hexdigest()}  {relative_path}\n")
    manifest_path.write_text("".join(lines), encoding="utf-8", newline="\n")
    return manifest_path


def assemble_package(
    root: Path,
    output_directory: Path,
    target: str,
    go_binary: Path,
    model_binary: Path,
) -> Path:
    if target not in TARGETS:
        raise ValueError(f"unsupported target: {target}")
    target_config = TARGETS[target]
    if output_directory.exists():
        shutil.rmtree(output_directory)
    output_directory.mkdir(parents=True)

    shutil.copy2(go_binary, output_directory / str(target_config["go_binary"]))
    shutil.copy2(model_binary, output_directory / str(target_config["model_binary"]))
    shutil.copy2(root / "words.json", output_directory / "words.json")

    models_output = output_directory / "models"
    for model_name in PRODUCTION_MODELS:
        shutil.copytree(root / "models" / model_name, models_output / model_name)

    common_directory = root / "packaging" / "common"
    shutil.copy2(common_directory / "config.env.example", output_directory / "config.env.example")
    shutil.copy2(common_directory / "README.txt", output_directory / "README.txt")
    launcher_directory = root / "packaging" / str(target_config["launcher_directory"])
    for launcher in launcher_directory.iterdir():
        if launcher.is_file():
            destination = output_directory / launcher.name
            shutil.copy2(launcher, destination)
            if target == "linux-amd64":
                destination.chmod(destination.stat().st_mode | 0o111)

    (output_directory / "data").mkdir()
    (output_directory / "logs").mkdir()
    write_manifest(output_directory)
    if target == "linux-amd64":
        for binary_name in (target_config["go_binary"], target_config["model_binary"]):
            binary_path = output_directory / str(binary_name)
            binary_path.chmod(binary_path.stat().st_mode | 0o111)
    return output_directory


def create_archive(
    package_directory: Path,
    target: str,
    dist_directory: Path,
) -> tuple[Path, Path]:
    if target not in TARGETS:
        raise ValueError(f"unsupported target: {target}")
    dist_directory.mkdir(parents=True, exist_ok=True)
    if target == "windows-amd64":
        archive_path = dist_directory / f"{package_directory.name}.zip"
        if archive_path.exists():
            archive_path.unlink()
        with zipfile.ZipFile(
            archive_path,
            "w",
            compression=zipfile.ZIP_DEFLATED,
            compresslevel=9,
        ) as zip_stream:
            for file_path in sorted(
                path for path in package_directory.rglob("*") if path.is_file()
            ):
                archive_name = (
                    Path(package_directory.name) / file_path.relative_to(package_directory)
                ).as_posix()
                zip_stream.write(file_path, archive_name)
    else:
        archive_path = dist_directory / f"{package_directory.name}.tar.gz"
        if archive_path.exists():
            archive_path.unlink()
        with tarfile.open(archive_path, "w:gz", compresslevel=9) as tar_stream:
            tar_stream.add(package_directory, arcname=package_directory.name)

    digest = hashlib.sha256()
    with archive_path.open("rb") as archive_stream:
        while chunk := archive_stream.read(HASH_CHUNK_SIZE):
            digest.update(chunk)
    checksum_path = archive_path.with_name(f"{archive_path.name}.sha256")
    checksum_target = archive_path.relative_to(dist_directory.parent).as_posix()
    checksum_path.write_text(
        f"{digest.hexdigest()}  {checksum_target}\n",
        encoding="ascii",
        newline="\n",
    )
    return archive_path, checksum_path


def reset_child_directory(path: Path, parent: Path) -> None:
    resolved_path = path.resolve()
    resolved_parent = parent.resolve()
    if resolved_path.parent != resolved_parent:
        raise ValueError(f"refusing to clean non-child path: {resolved_path}")
    if resolved_path.exists():
        shutil.rmtree(resolved_path)
    resolved_path.mkdir(parents=True)


def resolve_build_parent(root: Path) -> Path:
    configured = os.getenv("NOBLACK_BUILD_ROOT", "").strip()
    if configured:
        return Path(configured).expanduser().resolve()
    return (root / ".build").resolve()


def run_checked(command: list[str], root: Path, environment: dict[str, str] | None = None) -> None:
    print(f"[release] run: {' '.join(command)}", flush=True)
    subprocess.run(command, cwd=root, env=environment, check=True)


def build_go_binary(root: Path, target: str, build_directory: Path) -> Path:
    target_config = TARGETS[target]
    goos = "windows" if target == "windows-amd64" else "linux"
    output_path = build_directory / str(target_config["go_binary"])
    environment = os.environ.copy()
    environment.update(
        {
            "GOOS": goos,
            "GOARCH": "amd64",
            "CGO_ENABLED": "0",
            "GOCACHE": str(build_directory / "go-cache"),
            "GOTMPDIR": str(build_directory / "go-tmp"),
        }
    )
    Path(environment["GOCACHE"]).mkdir(parents=True, exist_ok=True)
    Path(environment["GOTMPDIR"]).mkdir(parents=True, exist_ok=True)
    run_checked(["go", "test", "./..."], root, environment)
    run_checked(
        [
            "go",
            "build",
            "-trimpath",
            "-ldflags=-s -w",
            "-o",
            str(output_path),
            "./cmd/server",
        ],
        root,
        environment,
    )
    return output_path


def native_target() -> str:
    if os.name == "nt":
        return "windows-amd64"
    if sys.platform.startswith("linux"):
        return "linux-amd64"
    raise RuntimeError(f"unsupported build platform: {sys.platform}")


def build_model_executable(root: Path, target: str, build_directory: Path) -> Path:
    current_target = native_target()
    if target != current_target:
        raise RuntimeError(
            f"model executable must be built natively: target={target}, current={current_target}"
        )
    pyinstaller_dist = build_directory / "pyinstaller-dist"
    pyinstaller_work = build_directory / "pyinstaller-work"
    run_checked(
        [
            sys.executable,
            "-m",
            "PyInstaller",
            "--noconfirm",
            "--clean",
            "--distpath",
            str(pyinstaller_dist),
            "--workpath",
            str(pyinstaller_work),
            str(root / "packaging" / "noblack-model.spec"),
        ],
        root,
    )
    model_name = str(TARGETS[target]["model_binary"])
    model_path = pyinstaller_dist / model_name
    if not model_path.is_file():
        raise FileNotFoundError(f"PyInstaller output missing: {model_path}")
    return model_path


def build_release(
    root: Path,
    target: str,
    prebuilt_model: Path | None = None,
) -> dict[str, str | int]:
    if target not in TARGETS:
        raise ValueError(f"unsupported target: {target}")
    validation = validate_release_inputs(root)
    build_parent = resolve_build_parent(root)
    dist_directory = root / "dist"
    build_parent.mkdir(exist_ok=True)
    dist_directory.mkdir(exist_ok=True)
    build_directory = build_parent / target
    reset_child_directory(build_directory, build_parent)

    go_binary = build_go_binary(root, target, build_directory)
    model_binary = (
        prebuilt_model.resolve()
        if prebuilt_model is not None
        else build_model_executable(root, target, build_directory)
    )
    if not model_binary.is_file():
        raise FileNotFoundError(f"model executable missing: {model_binary}")

    package_directory = dist_directory / f"noblack-{target}"
    assemble_package(root, package_directory, target, go_binary, model_binary)
    archive_path, checksum_path = create_archive(package_directory, target, dist_directory)
    archive_sha256 = checksum_path.read_text(encoding="ascii").split()[0]
    result: dict[str, str | int] = {
        **validation,
        "target": target,
        "package": str(package_directory),
        "archive": str(archive_path),
        "archive_size": archive_path.stat().st_size,
        "archive_sha256": archive_sha256,
    }
    print(json.dumps(result, ensure_ascii=False, indent=2), flush=True)
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description="Build portable Noblack releases")
    parser.add_argument("command", choices=("validate", "build"))
    parser.add_argument("--target", choices=tuple(TARGETS))
    parser.add_argument("--model-executable", type=Path)
    args = parser.parse_args()
    if args.command == "validate":
        print(json.dumps(validate_release_inputs(ROOT), ensure_ascii=False, indent=2))
    elif args.command == "build":
        if not args.target:
            parser.error("--target is required for build")
        build_release(ROOT, args.target, args.model_executable)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

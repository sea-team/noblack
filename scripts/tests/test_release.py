from __future__ import annotations

import tempfile
import unittest
import hashlib
import json
import tarfile
import zipfile
from unittest import mock
from pathlib import Path

from scripts import release


class ReleaseValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name)

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_validate_model_file_rejects_lfs_pointer(self) -> None:
        model = self.root / "models/lite-production-v1/model.safetensors"
        model.parent.mkdir(parents=True)
        model.write_text(
            "version https://git-lfs.github.com/spec/v1\n",
            encoding="utf-8",
        )

        with self.assertRaisesRegex(ValueError, "Git LFS pointer"):
            release.validate_model_file(
                self.root,
                str(model.relative_to(self.root)),
                10,
                "0" * 64,
            )

    def test_validate_model_file_rejects_wrong_digest(self) -> None:
        model = self.root / "model.safetensors"
        model.write_bytes(b"model")

        with self.assertRaisesRegex(ValueError, "SHA-256"):
            release.validate_model_file(
                self.root,
                "model.safetensors",
                5,
                "0" * 64,
            )

    def test_validate_model_file_returns_size_for_valid_file(self) -> None:
        model = self.root / "model.safetensors"
        model.write_bytes(b"model")

        result = release.validate_model_file(
            self.root,
            "model.safetensors",
            5,
            hashlib.sha256(b"model").hexdigest(),
        )

        self.assertEqual(result, 5)

    def test_validate_release_inputs_checks_production_models_and_words(self) -> None:
        words = self.root / "data" / "words.json"
        words.parent.mkdir()
        words.write_text('{"words":[]}\n', encoding="utf-8")
        checksums: dict[str, dict[str, int | str]] = {}
        for model_name, content in (
            ("lite-production-v1", b"lite-model"),
            ("macbert-production-v1", b"macbert-model"),
        ):
            model_directory = self.root / "models" / model_name
            model_directory.mkdir(parents=True)
            model_path = model_directory / "model.safetensors"
            model_path.write_bytes(content)
            (model_directory / "model_config.json").write_text(
                "{}\n",
                encoding="utf-8",
            )
            relative_path = model_path.relative_to(self.root).as_posix()
            checksums[relative_path] = {
                "size": len(content),
                "sha256": hashlib.sha256(content).hexdigest(),
            }
        packaging = self.root / "packaging"
        packaging.mkdir()
        (packaging / "model-checksums.json").write_text(
            json.dumps(checksums),
            encoding="utf-8",
        )

        result = release.validate_release_inputs(self.root)

        self.assertEqual(result["model_count"], 2)
        self.assertEqual(result["words_size"], words.stat().st_size)

    def test_validate_release_inputs_rejects_repository_words_duplicate(self) -> None:
        words = self.root / "data" / "words.json"
        words.parent.mkdir()
        words.write_text(
            '{"words":[]}\n',
            encoding="utf-8",
        )
        duplicate = self.root / "words.json"
        duplicate.write_text('{"words":[]}\n', encoding="utf-8")
        packaging = self.root / "packaging"
        packaging.mkdir()
        (packaging / "model-checksums.json").write_text(
            "{}\n",
            encoding="utf-8",
        )

        with self.assertRaisesRegex(
            ValueError,
            "root word database",
        ):
            release.validate_release_inputs(self.root)


class ReleaseAssemblyTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name) / "source"
        self.output = Path(self.temporary_directory.name) / "output"
        self.root.mkdir()
        words = self.root / "data" / "words.json"
        words.parent.mkdir()
        words.write_text('{"words":[]}\n', encoding="utf-8")
        for model_name in (
            "lite-baseline",
            "lite-production-v1",
            "macbert-pilot",
            "macbert-production-v1",
        ):
            model_directory = self.root / "models" / model_name
            model_directory.mkdir(parents=True)
            (model_directory / "model.safetensors").write_bytes(model_name.encode())
            (model_directory / "model_config.json").write_text("{}\n", encoding="utf-8")
        common = self.root / "packaging" / "common"
        common.mkdir(parents=True)
        (common / "config.env.example").write_text("NB_ADDR=:8080\n", encoding="utf-8")
        (common / "README.txt").write_text("Noblack\n", encoding="utf-8")
        linux = self.root / "packaging" / "linux"
        linux.mkdir()
        for launcher in ("start.sh", "start-keywords-only.sh", "stop.sh"):
            (linux / launcher).write_text("#!/usr/bin/env bash\n", encoding="utf-8")
        self.fake_go = Path(self.temporary_directory.name) / "noblack"
        self.fake_model = Path(self.temporary_directory.name) / "noblack-model"
        self.fake_go.write_bytes(b"go")
        self.fake_model.write_bytes(b"model")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_assemble_contains_only_production_models(self) -> None:
        release.assemble_package(
            self.root,
            self.output,
            "linux-amd64",
            self.fake_go,
            self.fake_model,
        )

        self.assertTrue(
            (
                self.output
                / "models/lite-production-v1/model.safetensors"
            ).is_file()
        )
        self.assertTrue(
            (
                self.output
                / "models/macbert-production-v1/model.safetensors"
            ).is_file()
        )
        self.assertFalse((self.output / "models/lite-baseline").exists())
        self.assertFalse((self.output / "models/macbert-pilot").exists())
        self.assertTrue((self.output / "data/words.json").is_file())
        self.assertFalse((self.output / "words.json").exists())

    def test_manifest_is_stable_and_excludes_itself(self) -> None:
        self.output.mkdir()
        (self.output / "a.txt").write_text("a\n", encoding="utf-8")
        nested = self.output / "nested"
        nested.mkdir()
        (nested / "b.txt").write_text("b\n", encoding="utf-8")

        release.write_manifest(self.output)
        first = (self.output / "SHA256SUMS").read_bytes()
        release.write_manifest(self.output)

        self.assertEqual(first, (self.output / "SHA256SUMS").read_bytes())
        self.assertNotIn(b"SHA256SUMS", first)
        self.assertIn(b"a.txt", first)
        self.assertIn(b"nested/b.txt", first)

    def test_create_linux_archive_and_checksum(self) -> None:
        package = Path(self.temporary_directory.name) / "noblack-linux-amd64"
        package.mkdir()
        (package / "noblack").write_bytes(b"go")
        dist = Path(self.temporary_directory.name) / "dist"

        archive, checksum = release.create_archive(package, "linux-amd64", dist)

        self.assertEqual(archive.name, "noblack-linux-amd64.tar.gz")
        self.assertTrue(checksum.is_file())
        self.assertEqual(
            checksum.read_text(encoding="ascii").split(maxsplit=1)[1].strip(),
            "dist/noblack-linux-amd64.tar.gz",
        )
        with tarfile.open(archive, "r:gz") as tar_stream:
            self.assertIn("noblack-linux-amd64/noblack", tar_stream.getnames())

    def test_create_windows_archive_and_checksum(self) -> None:
        package = Path(self.temporary_directory.name) / "noblack-windows-amd64"
        package.mkdir()
        (package / "noblack.exe").write_bytes(b"go")
        dist = Path(self.temporary_directory.name) / "dist"

        archive, checksum = release.create_archive(package, "windows-amd64", dist)

        self.assertEqual(archive.name, "noblack-windows-amd64.zip")
        self.assertTrue(checksum.is_file())
        with zipfile.ZipFile(archive) as zip_stream:
            self.assertIn("noblack-windows-amd64/noblack.exe", zip_stream.namelist())

    def test_build_parent_can_use_fast_external_directory(self) -> None:
        external = Path(self.temporary_directory.name) / "fast-build"
        with mock.patch.dict(
            "os.environ",
            {"NOBLACK_BUILD_ROOT": str(external)},
        ):
            self.assertEqual(
                release.resolve_build_parent(self.root),
                external.resolve(),
            )

    def test_windows_cross_build_runs_tests_in_host_environment(self) -> None:
        build_directory = Path(self.temporary_directory.name) / "build"
        build_directory.mkdir()

        with mock.patch.object(release, "run_checked") as run_checked:
            output = release.build_go_binary(
                self.root,
                "windows-amd64",
                build_directory,
            )

        self.assertEqual(output, build_directory / "noblack.exe")
        self.assertEqual(len(run_checked.call_args_list), 2)
        test_command, test_root, test_environment = run_checked.call_args_list[0].args
        build_command, build_root, build_environment = run_checked.call_args_list[1].args
        self.assertEqual(test_command, ["go", "test", "./..."])
        self.assertEqual(test_root, self.root)
        self.assertNotIn("GOOS", test_environment)
        self.assertNotIn("GOARCH", test_environment)
        self.assertNotIn("CGO_ENABLED", test_environment)
        self.assertEqual(build_command[0:2], ["go", "build"])
        self.assertEqual(build_root, self.root)
        self.assertEqual(build_environment["GOOS"], "windows")
        self.assertEqual(build_environment["GOARCH"], "amd64")
        self.assertEqual(build_environment["CGO_ENABLED"], "0")


if __name__ == "__main__":
    unittest.main()

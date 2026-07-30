from __future__ import annotations

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WINDOWS = ROOT / "packaging" / "windows"


class WindowsLauncherContractTests(unittest.TestCase):
    def read(self, name: str) -> str:
        return (WINDOWS / name).read_text(encoding="utf-8-sig")

    def test_cmd_entrypoints_are_package_relative(self) -> None:
        for name in ("start.cmd", "start-keywords-only.cmd", "stop.cmd"):
            source = self.read(name).lower()
            self.assertIn("%~dp0", source)
            self.assertIn("powershell.exe", source)
            self.assertIn("-noprofile", source)
            self.assertIn("-executionpolicy bypass", source)

    def test_controller_starts_processes_and_checks_health(self) -> None:
        source = self.read("noblack-control.ps1")

        self.assertIn("$PSScriptRoot", source)
        self.assertIn("Start-Process", source)
        self.assertIn("-PassThru", source)
        self.assertIn("Get-CimInstance Win32_Process", source)
        self.assertIn("Invoke-RestMethod", source)
        self.assertIn("data\\noblack.pid", source)
        self.assertIn("data\\noblack-model.pid", source)

    def test_controller_never_stops_processes_by_image_name(self) -> None:
        source = self.read("noblack-control.ps1").lower()

        self.assertIn("stop-process -id", source)
        self.assertNotIn("taskkill /im", source)
        self.assertNotIn("stop-process -name", source)

    def test_controller_uses_single_data_word_database(self) -> None:
        source = self.read("noblack-control.ps1")

        self.assertIn('$wordsFile = Join-Path $DataDir "words.json"', source)
        self.assertNotIn('Join-Path $Root "words.json"', source)
        self.assertNotIn("Copy-Item", source)
        self.assertIn("word database is missing", source)

    def test_watch_argument_is_one_formatted_array_item(self) -> None:
        source = self.read("noblack-control.ps1")

        self.assertIn(
            '$watch = Get-NoblackSetting "NB_WATCH" "true"',
            source,
        )
        self.assertIn('("-watch={0}" -f $watch)', source)

    def test_model_pid_tracks_the_process_that_owns_the_port(self) -> None:
        source = self.read("noblack-control.ps1")

        self.assertIn("function Get-VerifiedListeningProcess", source)
        self.assertIn(
            "$modelListener = Get-VerifiedListeningProcess "
            "$modelPort $ModelExecutable",
            source,
        )
        self.assertIn(
            "Set-Content -LiteralPath $ModelPidFile "
            "-Value $modelListener.Id",
            source,
        )


if __name__ == "__main__":
    unittest.main()

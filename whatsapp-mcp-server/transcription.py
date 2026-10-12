"""Voice-note transcription with macOS's on-device speech recognizer.

The recognizer (SpeechAnalyzer, macOS 26+) is only reachable from Swift, so
transcribe.swift is compiled into bin/transcribe on first use and again
whenever the source changes.
"""
import os
import shutil
import subprocess
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
SOURCE = os.path.join(HERE, "transcribe.swift")
BINARY = os.path.join(HERE, "bin", "transcribe")
LOCALE = os.environ.get("WHATSAPP_TRANSCRIBE_LOCALE", "pt_BR")


def _helper() -> str:
    if not os.path.exists(BINARY) or os.path.getmtime(BINARY) < os.path.getmtime(SOURCE):
        os.makedirs(os.path.dirname(BINARY), exist_ok=True)
        build = subprocess.run(
            ["xcrun", "swiftc", "-parse-as-library", "-O", "-o", BINARY, SOURCE],
            capture_output=True,
            text=True,
        )
        if build.returncode != 0:
            raise RuntimeError(f"could not compile the transcriber (needs Xcode command line tools): {build.stderr.strip()}")
    return BINARY


def _run(path: str) -> str:
    result = subprocess.run([_helper(), path, LOCALE], capture_output=True, text=True, timeout=600)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or f"transcriber exited with {result.returncode}")
    return result.stdout.strip()


def transcribe_file(path: str) -> str:
    """Transcribe an audio file and return its text.

    macOS reads WhatsApp's Ogg/Opus directly but fails on some of those files
    ("nilError"), so a failure is retried on a WAV copy decoded by ffmpeg.
    """
    try:
        return _run(path)
    except RuntimeError:
        if not shutil.which("ffmpeg"):
            raise
    with tempfile.TemporaryDirectory() as tmp:
        wav = os.path.join(tmp, "audio.wav")
        subprocess.run(
            ["ffmpeg", "-v", "error", "-y", "-i", path, "-ar", "16000", "-ac", "1", wav],
            check=True,
            capture_output=True,
        )
        return _run(wav)

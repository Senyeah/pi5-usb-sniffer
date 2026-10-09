"""L5: isochronous IN audio to WAV files, one file per stream."""

from __future__ import annotations

import wave
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path

PACKET_S = 0.001  # UAC1 iso packets are 1 ms at full and high speed
FILL_MIN_S = 0.02  # shorter pauses are URB completion jitter
SPLIT_S = 5.0  # longer pauses start a new file; shorter ones become silence


@dataclass
class AudioStream:
    path: Path | None
    rate: int
    channels: int
    bytes_per_sample: int
    t_start: float
    t_end: float = 0.0
    packets: int = 0
    error_packets: int = 0
    empty_packets: int = 0
    odd_packets: int = 0  # length not a whole number of frames
    gaps: int = 0
    gap_s: float = 0.0
    frames: int = 0
    errors: dict[int, int] = field(default_factory=dict)
    start_reason: str = ""
    end_reason: str = ""

    @property
    def audio_s(self) -> float:
        return self.frames / self.rate if self.rate else 0.0

    def summary(self) -> dict:
        return {
            "file": self.path.name if self.path else None,
            "start_utc": datetime.fromtimestamp(self.t_start, UTC)
            .isoformat(timespec="milliseconds")
            .replace("+00:00", "Z"),
            "rate_hz": self.rate,
            "channels": self.channels,
            "bits": self.bytes_per_sample * 8,
            "wall_s": round(self.t_end - self.t_start, 3),
            "audio_s": round(self.audio_s, 3),
            "packets": self.packets,
            "error_packets": self.error_packets,
            "errors": {str(k): v for k, v in sorted(self.errors.items())},
            "empty_packets": self.empty_packets,
            "odd_packets": self.odd_packets,
            "gaps": self.gaps,
            "gap_s": round(self.gap_s, 3),
            "start": self.start_reason,
            "end": self.end_reason,
        }


class AudioWriter:
    def __init__(self, out_dir: Path | None) -> None:
        self.out_dir = out_dir
        self.streams: list[AudioStream] = []
        self._cur: AudioStream | None = None
        self._wav: wave.Wave_write | None = None

    @property
    def active(self) -> AudioStream | None:
        return self._cur

    def start(self, t: float, rate: int, channels: int, bytes_per_sample: int, reason: str) -> AudioStream:
        if self._cur is not None:
            self.stop(t, "new stream")
        path = None
        if self.out_dir is not None:
            path = self.out_dir / f"audio-{len(self.streams) + 1:02d}.wav"
            self._wav = wave.open(str(path), "wb")  # noqa: SIM115 - open across feed() calls
            self._wav.setnchannels(channels)
            self._wav.setsampwidth(bytes_per_sample)
            self._wav.setframerate(rate)
        self._cur = AudioStream(path, rate, channels, bytes_per_sample, t, t, start_reason=reason)
        self.streams.append(self._cur)
        return self._cur

    def stop(self, t: float, reason: str) -> AudioStream | None:
        s = self._cur
        if s is None:
            return None
        s.end_reason = reason
        if self._wav is not None:
            self._wav.close()
            self._wav = None
        self._cur = None
        return s

    def gap(self, t: float) -> bool:
        return self._cur is not None and t - self._cur.t_end > SPLIT_S

    def feed(self, t: float, packets: list[tuple[int, bytes]]) -> None:
        s = self._cur
        if s is None:
            return
        frame = s.channels * s.bytes_per_sample
        chunks = []
        if s.packets:
            # A late completion means no URB was queued, so no audio crossed in that time.
            pause = t - s.t_end - len(packets) * PACKET_S
            if pause > FILL_MIN_S:
                n = round(pause * s.rate)
                chunks.append(bytes(n * frame))
                s.gaps += 1
                s.gap_s += pause
                s.frames += n
        for status, data in packets:
            s.packets += 1
            if status != 0:
                s.error_packets += 1
                s.errors[status] = s.errors.get(status, 0) + 1
                continue
            if not data:
                s.empty_packets += 1
                continue
            if len(data) % frame:
                s.odd_packets += 1
                data = data[: len(data) - len(data) % frame]
            chunks.append(data)
            s.frames += len(data) // frame
        if chunks and self._wav is not None:
            self._wav.writeframesraw(b"".join(chunks))
        s.t_end = t

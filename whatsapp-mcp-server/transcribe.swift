// Transcribes an audio file with macOS's on-device speech recognizer
// (SpeechAnalyzer, macOS 26+). Nothing leaves the machine.
//
// Usage: transcribe <audio file> [locale, default pt_BR]
// Prints the transcription to stdout; errors go to stderr with exit code 1.
// transcription.py compiles this on first use.

import AVFoundation
import Foundation
import Speech

struct TranscribeError: Error, CustomStringConvertible {
    let description: String
}

@main
struct Transcribe {
    static func main() async {
        let args = CommandLine.arguments
        guard args.count >= 2 else {
            fail("usage: transcribe <audio file> [locale]")
        }
        do {
            print(try await transcribe(URL(fileURLWithPath: args[1]), locale: args.count > 2 ? args[2] : "pt_BR"))
        } catch {
            fail("\(error)")
        }
    }

    static func transcribe(_ url: URL, locale identifier: String) async throws -> String {
        guard SpeechTranscriber.isAvailable else {
            throw TranscribeError(description: "speech transcription is not available on this Mac")
        }
        guard let locale = await SpeechTranscriber.supportedLocale(equivalentTo: Locale(identifier: identifier)) else {
            throw TranscribeError(description: "locale \(identifier) is not supported")
        }
        let transcriber = SpeechTranscriber(locale: locale, preset: .transcription)
        // The language model is usually already installed; download it otherwise
        if let request = try await AssetInventory.assetInstallationRequest(supporting: [transcriber]) {
            try await request.downloadAndInstall()
        }

        let analyzer = SpeechAnalyzer(modules: [transcriber])
        let file = try AVAudioFile(forReading: url)
        async let text = transcriber.results.reduce("") { $0 + String($1.text.characters) }
        if let end = try await analyzer.analyzeSequence(from: file) {
            try await analyzer.finalizeAndFinish(through: end)
        } else {
            await analyzer.cancelAndFinishNow()
        }
        return try await text.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    static func fail(_ message: String) -> Never {
        FileHandle.standardError.write(Data((message + "\n").utf8))
        exit(1)
    }
}

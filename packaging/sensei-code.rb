# Homebrew formula for the globulario/tap tap.
#
# Copy to globulario/homebrew-tap/Formula/sensei-code.rb when cutting a release
# and fill in the release SHAs. It depends on sensei rather than merely
# suggesting it: sensei-code reads Sensei's graph and does very little without
# it, so installing one without the other leaves a broken tool.
class SenseiCode < Formula
  desc "Terminal workspace for governed AI software development with Sensei"
  homepage "https://github.com/globulario/sensei-code"
  version "0.1.0"
  license "AGPL-3.0-only"

  depends_on "globulario/tap/sensei"

  on_macos do
    on_arm do
      url "https://github.com/globulario/sensei-code/releases/download/v0.1.0/sensei-code-darwin-arm64.tar.gz"
      sha256 "REPLACE_WITH_RELEASE_SHA256"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/globulario/sensei-code/releases/download/v0.1.0/sensei-code-linux-amd64.tar.gz"
      sha256 "REPLACE_WITH_RELEASE_SHA256"
    end
    on_arm do
      url "https://github.com/globulario/sensei-code/releases/download/v0.1.0/sensei-code-linux-arm64.tar.gz"
      sha256 "REPLACE_WITH_RELEASE_SHA256"
    end
  end

  def install
    bin.install "sensei-code"
  end

  def caveats
    <<~EOS
      In a repository, check and repair everything a session needs:

        sensei-code setup --apply

      Then start it with `sensei-code`. Setup reports what is wrong, what you
      would see when it breaks, and the command that fixes it.
    EOS
  end

  test do
    assert_match "Sensei Code", shell_output("#{bin}/sensei-code help")
  end
end

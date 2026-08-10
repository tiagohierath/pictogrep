{
  description = "pictogrep";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
  let
    system = "x86_64-linux";
    pkgs = import nixpkgs {
      inherit system;
    };
  in {
    devShells.${system}.default = pkgs.mkShell {
      packages = [
        pkgs.python312
        pkgs.python312Packages.pip
        pkgs.python312Packages.virtualenv
        pkgs.python312Packages.numpy
        pkgs.python312Packages.pillow
        pkgs.stdenv.cc.cc.lib
        pkgs.zlib
        pkgs.mpv
      ];
      shellHook = ''
        export PICTOGREP_LIBSTDCPP="${pkgs.lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib pkgs.zlib ]}"
        export LD_LIBRARY_PATH="$PICTOGREP_LIBSTDCPP''${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
      '';
    };
  };
}

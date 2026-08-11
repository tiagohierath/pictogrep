{
  description = "Local image search and storyboarding without runtime dependencies";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = function:
        nixpkgs.lib.genAttrs systems (system: function (import nixpkgs { inherit system; }));
    in {
      packages = forAllSystems (pkgs:
        let
          desktopItem = pkgs.makeDesktopItem {
            name = "pictogrep";
            desktopName = "Pictogrep";
            comment = "Find and storyboard your pictures";
            exec = "pictogrep";
            icon = "pictogrep";
            terminal = false;
            categories = [ "Graphics" "Photography" ];
          };
        in {
          default = pkgs.buildGoModule {
            pname = "pictogrep";
            version = "0.4.0";
            src = self;
            vendorHash = null;
            subPackages = [ "." ];
            ldflags = [ "-s" "-w" ];
            postInstall = ''
              mkdir -p $out/share/applications
              cp ${desktopItem}/share/applications/pictogrep.desktop $out/share/applications/
              mkdir -p $out/share/icons/hicolor/512x512/apps
              cp assets/pictogrep.png $out/share/icons/hicolor/512x512/apps/pictogrep.png
            '';
            meta = {
              description = "Local image search and storyboarding application";
              homepage = "https://github.com/tiagohierath/pictogrep";
              mainProgram = "pictogrep";
              platforms = pkgs.lib.platforms.linux;
            };
          };
        });

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.default}/bin/pictogrep";
          meta.description = "Open Pictogrep";
        };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.python312
            pkgs.python312Packages.pip
            pkgs.python312Packages.virtualenv
            pkgs.stdenv.cc.cc.lib
            pkgs.zlib
            pkgs.mpv
          ];
          shellHook = ''
            export PICTOGREP_LIBSTDCPP="${pkgs.lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib pkgs.zlib ]}"
            export LD_LIBRARY_PATH="$PICTOGREP_LIBSTDCPP''${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
          '';
        };
      });
    };
}

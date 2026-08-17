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
          # Keep in step with `var version` in app.go; a Go test guards the pair.
          version = "0.8.6";
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
            inherit version;
            src = self;
            vendorHash = null;
            subPackages = [ "." ];
            ldflags = [ "-s" "-w" "-X" "main.version=${version}" ];
            nativeBuildInputs = [ pkgs.makeWrapper ];
            postInstall = ''
              mkdir -p $out/share/applications
              cp ${desktopItem}/share/applications/pictogrep.desktop $out/share/applications/
              mkdir -p $out/share/icons/hicolor/512x512/apps
              cp assets/pictogrep.png $out/share/icons/hicolor/512x512/apps/pictogrep.png
            '';
            postFixup = ''
              wrapProgram $out/bin/pictogrep \
                --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.gallery-dl ]}
            '';
            meta = {
              description = "Local image search and storyboarding application";
              homepage = "https://navylily.tv/pictogrep";
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
          ];
        };
      });
    };
}

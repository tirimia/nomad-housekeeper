{
  description = "Nomad Housekeeper";

  inputs = {
    nixpkgs.url = "github:nixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    flake-compat = {
      url = "github:inclyc/flake-compat";
      flake = false;
    };
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = import nixpkgs {inherit system;};
        name = "nomad-housekeeper";
        goPlatform = {
          "aarch64-darwin" = {
            GOOS = "darwin";
            GOARCH = "arm64";
          };
          "aarch64-linux" = {
            GOOS = "linux";
            GOARCH = "arm64";
          };
          "x86-64-darwin" = {
            GOOS = "darwin";
            GOARCH = "amd64";
          };
          "x86-64-linux" = {
            GOOS = "linux";
            GOARCH = "amd64";
          };
        };
        housekeeperRecipe = {
          GOOS,
          GOARCH,
          ...
        } @ args:
          (pkgs.buildGoModule {
            inherit name GOOS GOARCH;
            pname = name;
            src = ./.;
            vendorHash = "sha256-6nK1irhFK9wE/fKWh1NrSn0maznXLAUddhuVcO3FFUw=";
            CGO_ENABLED = 0;

            # Typically wouldn't do this, but otherwise buildGoModule insists on
            # messing with the outppath when cross-compiling
            buildPhase = ''
              mkdir -p $out/bin
              go build -o $out/bin/nomad-housekeeper
            '';
          }).overrideAttrs (old: old // args);
        linuxSystem = (pkgs.lib.removeSuffix "darwin" system) + "linux";
      in {
        formatter = pkgs.alejandra;
        devShells.default = pkgs.mkShell {
          buildInputs = builtins.attrValues {inherit (pkgs) go nomad;};
        };
        packages = {
          nomad-housekeeper = housekeeperRecipe goPlatform.${system};
          testing = housekeeperRecipe goPlatform.${linuxSystem};
          default = self.packages.${system}.nomad-housekeeper;
          dockerContainer =
            pkgs.dockerTools.buildImage
            {
              # TODO: figure out how to get container tar to change name
              name = "nomad-housekeeper";
              tag = "latest"; # TODO: add versioning

              copyToRoot = pkgs.buildEnv {
                name = "image-root";
                paths = [(housekeeperRecipe goPlatform.${linuxSystem})];
                pathsToLink = ["/bin"];
              };

              config = {
                Cmd = ["/bin/nomad-housekeeper"];
                ExposedPorts = {"8080/tcp" = {};};
              };
            };
        };
        # TODO: sample nomad job files
        # TODO: github actions build releases - also build nightly
        # TODO: add a vmtest
      }
    );
}

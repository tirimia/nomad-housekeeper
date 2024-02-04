#
default:
    @just -l

buildDocker:
    @nix build '.#dockerContainer'

runDocker: buildDocker
    @docker load < result
    @docker run --rm nomad-housekeeper:latest

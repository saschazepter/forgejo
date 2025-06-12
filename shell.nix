{
  pkgs ? import <nixpkgs> { },
}:

pkgs.mkShell {
  name = "forgejo";
  nativeBuildInputs = with pkgs; [
    # generic
    git
    git-lfs
    gnumake
    gnused
    gnutar
    gzip

    # frontend
    nodejs

    # backend
    gofumpt
    sqlite
    go
    gopls

    # tests
    openssh
  ];
}

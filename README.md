# Go-import-redirector

*Based and forked from https://godoc.org/rsc.io/go-import-redirector*

This repository enables multiple imports redirecting read from a file, and a docker options to run as a container. 

##Clone 
go get -v -u github.com/noaleibo1/go-import-redirector

##Usage
All previous functionality remains as explained here: *https://godoc.org/rsc.io/go-import-redirector*.

To use multiple imports redirection read from file, use the example in ```config_imports.txt```.

###Docker
1. Use ```make build-docker``` to create the image from the repository.
2. Run ```docker run go-import-redirector``` to create the container.

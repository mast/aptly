package debian

import (
	"bufio"
	"fmt"
	"github.com/smira/aptly/utils"
	"path/filepath"
	"strings"
	"time"
)

// PublishedRepo is a published for http/ftp representation of snapshot as Debian repository
type PublishedRepo struct {
	// Prefix & distribution should be unique across all published repositories
	Prefix       string
	Distribution string
	Component    string
	// Architectures is a list of all architectures published
	Architectures []string
	// Snapshot as a source of publishing
	SnapshotUUID string

	snapshot *Snapshot
}

// NewPublishedRepo creates new published repository
func NewPublishedRepo(prefix string, distribution string, component string, architectures []string, snapshot *Snapshot) *PublishedRepo {
	return &PublishedRepo{
		Prefix:        prefix,
		Distribution:  distribution,
		Component:     component,
		Architectures: architectures,
		SnapshotUUID:  snapshot.UUID,
		snapshot:      snapshot,
	}
}

// Publish publishes snapshot (repository) contents, links package files, generates Packages & Release files, signs them
func (p *PublishedRepo) Publish(repo *Repository, packageCollection *PackageCollection, signer utils.Signer) error {
	err := repo.MkDir(filepath.Join(p.Prefix, "pool"))
	if err != nil {
		return err
	}
	basePath := filepath.Join(p.Prefix, "dists", p.Distribution)
	err = repo.MkDir(basePath)
	if err != nil {
		return err
	}

	// Load all packages
	list, err := NewPackageListFromRefList(p.snapshot.RefList(), packageCollection)
	if err != nil {
		return fmt.Errorf("unable to load packages: %s", err)
	}

	if list.Len() == 0 {
		return fmt.Errorf("repository is empty, can't publish")
	}

	if p.Architectures == nil {
		p.Architectures = list.Architectures()
	}

	if len(p.Architectures) == 0 {
		return fmt.Errorf("unable to figure out list of architectures, please supply explicit list")
	}

	generatedFiles := map[string]*utils.ChecksumInfo{}

	// For all architectures, generate release file
	for _, arch := range p.Architectures {
		relativePath := filepath.Join(p.Component, fmt.Sprintf("binary-%s", arch), "Packages")
		err = repo.MkDir(filepath.Dir(filepath.Join(basePath, relativePath)))
		if err != nil {
			return err
		}

		packagesFile, err := repo.CreateFile(filepath.Join(basePath, relativePath))
		if err != nil {
			return fmt.Errorf("unable to creates Packages file: %s", err)
		}

		bufWriter := bufio.NewWriter(packagesFile)

		err = list.ForEach(func(pkg *Package) error {
			if pkg.MatchesArchitecture(arch) {
				err = pkg.LinkFromPool(repo, p.Prefix, p.Component)
				if err != nil {
					return err
				}

				err = pkg.Stanza().WriteTo(bufWriter)
				if err != nil {
					return err
				}
				err = bufWriter.WriteByte('\n')
				if err != nil {
					return err
				}

			}

			return nil
		})

		if err != nil {
			return fmt.Errorf("unable to creates process packages: %s", err)
		}

		err = bufWriter.Flush()
		if err != nil {
			return fmt.Errorf("unable to write Packages file: %s", err)
		}

		err = utils.CompressFile(packagesFile)
		if err != nil {
			return fmt.Errorf("unable to compress Packages files: %s", err)
		}

		packagesFile.Close()

		checksumInfo, err := repo.ChecksumsForFile(filepath.Join(basePath, relativePath))
		if err != nil {
			return fmt.Errorf("unable to collect checksums: %s", err)
		}
		generatedFiles[relativePath] = checksumInfo

		checksumInfo, err = repo.ChecksumsForFile(filepath.Join(basePath, relativePath+".gz"))
		if err != nil {
			return fmt.Errorf("unable to collect checksums: %s", err)
		}
		generatedFiles[relativePath+".gz"] = checksumInfo

		checksumInfo, err = repo.ChecksumsForFile(filepath.Join(basePath, relativePath+".bz2"))
		if err != nil {
			return fmt.Errorf("unable to collect checksums: %s", err)
		}
		generatedFiles[relativePath+".bz2"] = checksumInfo

	}

	release := make(Stanza)
	release["Origin"] = p.Prefix + " " + p.Distribution
	release["Label"] = p.Prefix + " " + p.Distribution
	release["Codename"] = p.Distribution
	release["Date"] = time.Now().UTC().Format("Mon, 2 Jan 2006 15:04:05 MST")
	release["Components"] = p.Component
	release["Architectures"] = strings.Join(p.Architectures, " ")
	release["Description"] = "Generated by aptly\n"
	release["MD5Sum"] = "\n"
	release["SHA1"] = "\n"
	release["SHA256"] = "\n"

	for path, info := range generatedFiles {
		release["MD5Sum"] += fmt.Sprintf(" %s %8d %s\n", info.MD5, info.Size, path)
		release["SHA1"] += fmt.Sprintf(" %s %8d %s\n", info.SHA1, info.Size, path)
		release["SHA256"] += fmt.Sprintf(" %s %8d %s\n", info.SHA256, info.Size, path)
	}

	releaseFile, err := repo.CreateFile(filepath.Join(basePath, "Release"))
	if err != nil {
		return fmt.Errorf("unable to create Release file: %s", err)
	}

	bufWriter := bufio.NewWriter(releaseFile)

	err = release.WriteTo(bufWriter)
	if err != nil {
		return fmt.Errorf("unable to create Release file: %s", err)
	}

	err = bufWriter.Flush()
	if err != nil {
		return fmt.Errorf("unable to create Release file: %s", err)
	}

	releaseFilename := releaseFile.Name()
	releaseFile.Close()

	err = signer.DetachedSign(releaseFilename, releaseFilename+".gpg")
	if err != nil {
		return fmt.Errorf("unable to sign Release file: %s", err)
	}

	err = signer.ClearSign(releaseFilename, filepath.Join(filepath.Dir(releaseFilename), "InRelease"))
	if err != nil {
		return fmt.Errorf("unable to sign Release file: %s", err)
	}

	return nil
}

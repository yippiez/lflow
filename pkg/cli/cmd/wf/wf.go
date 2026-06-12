/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package wf provides the workflowy integration commands: mirror, list,
// pull/push, unmirror and login.
package wf

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/lflow/lflow/pkg/cli/utils"
	wfpkg "github.com/lflow/lflow/pkg/cli/wf"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var dim = color.New(color.FgHiBlack)
var red = color.New(color.FgRed)

// NewCmd returns the wf command group
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wf",
		Short: "Workflowy integration",
	}

	cmd.AddCommand(newLoginCmd(ctx))
	cmd.AddCommand(newMirrorCmd(ctx))
	cmd.AddCommand(newListCmd(ctx))
	cmd.AddCommand(newSyncCmd(ctx, "pull"))
	cmd.AddCommand(newSyncCmd(ctx, "push"))
	cmd.AddCommand(newUnmirrorCmd(ctx))

	return cmd
}

func newLoginCmd(ctx context.DnoteCtx) *cobra.Command {
	var sessionFlag, baseURLFlag string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to workflowy, or store a session id directly",
		RunE: func(cmd *cobra.Command, args []string) error {
			if baseURLFlag != "" {
				if err := database.UpsertSystem(ctx.DB, wfpkg.SystemWfBaseURL, baseURLFlag); err != nil {
					return err
				}
			}

			session := sessionFlag
			if session == "" {
				var username string
				fmt.Print("workflowy email: ")
				fmt.Scanln(&username)
				fmt.Print("password: ")
				pw, err := term.ReadPassword(int(syscall.Stdin))
				fmt.Println()
				if err != nil {
					return errors.Wrap(err, "reading password")
				}

				var baseURL string
				database.GetSystem(ctx.DB, wfpkg.SystemWfBaseURL, &baseURL)
				session, err = wfpkg.Login(baseURL, username, string(pw))
				if err != nil {
					return err
				}
			}

			if err := database.UpsertSystem(ctx.DB, wfpkg.SystemWfSession, session); err != nil {
				return err
			}

			log.Success("logged in to workflowy\n")
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&sessionFlag, "session", "", "store a workflowy sessionid directly (for 2FA accounts)")
	f.StringVar(&baseURLFlag, "base-url", "", "override the workflowy endpoint (testing/self-hosted)")

	return cmd
}

func newMirrorCmd(ctx context.DnoteCtx) *cobra.Command {
	var intoFlag string

	cmd := &cobra.Command{
		Use:   "mirror <url|wf-id>",
		Short: "Anchor a workflowy node into the local tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("missing workflowy node url or id")
			}
			wfRef := wfpkg.ParseNodeRef(args[0])
			db := ctx.DB

			client, err := wfpkg.ClientFromCtx(ctx)
			if err != nil {
				return err
			}

			root, _, err := client.FetchTree()
			if err != nil {
				return errors.Wrap(err, "fetching workflowy tree")
			}
			wfNode, ok := wfpkg.FindByID(root, wfRef)
			if !ok {
				fmt.Println(red.Sprint("→ ") + fmt.Sprintf("no workflowy node matching %q", wfRef))
				os.Exit(1)
			}

			// the local anchor node: under --into, or as a new root
			parentUUID := ""
			parentName := "root"
			if intoFlag != "" {
				r, err := resolve.Resolve(db, intoFlag)
				if err != nil {
					if _, isMiss := err.(resolve.ErrNoMatch); isMiss {
						resolve.PrintNoMatch(intoFlag)
						os.Exit(1)
					}
					return err
				}
				parentUUID = r.Node.UUID
				parentName = r.Node.Name
			}

			anchor, err := createAnchorNode(db, parentUUID, wfNode.Name)
			if err != nil {
				return err
			}
			if err := wfpkg.CreateMirror(db, anchor, wfNode.ID); err != nil {
				return err
			}

			syncer := &wfpkg.Syncer{DB: db, Client: client, Journal: wfpkg.JournalFromCtx(ctx)}
			res, err := syncer.Sync(anchor, time.Now().Unix())
			if err != nil {
				return errors.Wrap(err, "initial mirror sync")
			}

			log.Successf("mirroring %q → %s %s\n", wfNode.Name, parentName, dim.Sprintf("· %d pulled", res.Pulled))
			return nil
		},
	}

	cmd.Flags().StringVar(&intoFlag, "into", "", "local parent node (default: a new root)")

	return cmd
}

func createAnchorNode(db *database.DB, parentUUID, name string) (string, error) {
	rank, err := database.NextRank(db, parentUUID)
	if err != nil {
		return "", err
	}
	uuid, err := utils.GenerateUUID()
	if err != nil {
		return "", err
	}
	now := time.Now().UnixNano()
	n := database.Node{
		UUID:       uuid,
		ParentUUID: parentUUID,
		Rank:       rank,
		Name:       name,
		Layout:     database.LayoutBullets,
		AddedOn:    now,
		EditedOn:   now,
		Dirty:      true,
	}
	if err := n.Insert(db); err != nil {
		return "", err
	}
	return uuid, nil
}

func newListCmd(ctx context.DnoteCtx) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List workflowy mirrors",
		RunE: func(cmd *cobra.Command, args []string) error {
			db := ctx.DB
			mirrors, err := wfpkg.GetMirrors(db)
			if err != nil {
				return err
			}
			if len(mirrors) == 0 {
				fmt.Println(dim.Sprint("no workflowy mirrors · lflow wf mirror <url>"))
				return nil
			}
			for _, m := range mirrors {
				node, err := database.GetNode(db, m.NodeUUID)
				if err != nil {
					continue
				}
				count, err := database.CountSubtree(db, m.NodeUUID)
				if err != nil {
					count = 1
				}
				ago := "never"
				if m.LastSync > 0 {
					ago = fmt.Sprintf("%s ago", time.Since(time.Unix(m.LastSync, 0)).Round(time.Second))
				}
				fmt.Printf("  %s %-24s %s\n", red.Sprint("◆"), node.Name,
					dim.Sprintf("workflowy · last sync %s · %s", ago, resolve.CountNoun(count, "node")))
			}
			return nil
		},
	}
}

func newSyncCmd(ctx context.DnoteCtx, name string) *cobra.Command {
	short := "Pull workflowy changes into the mirrors"
	if name == "push" {
		short = "Push local mirror changes to workflowy"
	}
	return &cobra.Command{
		Use:   name + " [mirror]",
		Short: short + " (both directions run either way)",
		RunE: func(cmd *cobra.Command, args []string) error {
			db := ctx.DB
			client, err := wfpkg.ClientFromCtx(ctx)
			if err != nil {
				return err
			}
			syncer := &wfpkg.Syncer{DB: db, Client: client, Journal: wfpkg.JournalFromCtx(ctx)}

			var anchors []string
			if len(args) > 0 {
				r, err := resolve.Resolve(db, args[0])
				if err != nil {
					if _, isMiss := err.(resolve.ErrNoMatch); isMiss {
						resolve.PrintNoMatch(args[0])
						os.Exit(1)
					}
					return err
				}
				anchors = append(anchors, r.Node.UUID)
			} else {
				mirrors, err := wfpkg.GetMirrors(db)
				if err != nil {
					return err
				}
				for _, m := range mirrors {
					anchors = append(anchors, m.NodeUUID)
				}
			}

			if len(anchors) == 0 {
				fmt.Println(dim.Sprint("no workflowy mirrors to sync"))
				return nil
			}

			for _, anchor := range anchors {
				res, err := syncer.Sync(anchor, time.Now().Unix())
				if err != nil {
					return err
				}
				conflictNote := ""
				if res.Conflicts > 0 {
					conflictNote = dim.Sprintf(" · %d conflicts journaled", res.Conflicts)
				}
				log.Successf("pulled %d changed %s\n", res.Pulled, dim.Sprintf("· pushed %d", res.Pushed)+conflictNote)
			}
			return nil
		},
	}
}

func newUnmirrorCmd(ctx context.DnoteCtx) *cobra.Command {
	var keep, drop bool

	cmd := &cobra.Command{
		Use:   "unmirror <mirror>",
		Short: "Detach a workflowy mirror, keeping or dropping the local copy",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("missing mirror reference")
			}
			if !keep && !drop {
				return errors.New("pass --keep to keep the local copy or --drop to delete it")
			}
			db := ctx.DB

			r, err := resolve.Resolve(db, args[0])
			if err != nil {
				if _, isMiss := err.(resolve.ErrNoMatch); isMiss {
					resolve.PrintNoMatch(args[0])
					os.Exit(1)
				}
				return err
			}

			if err := wfpkg.RemoveMirror(db, r.Node.UUID, drop); err != nil {
				return err
			}

			if drop {
				log.Successf("unmirrored and dropped %q\n", r.Node.Name)
			} else {
				log.Successf("unmirrored %q (local copy kept)\n", r.Node.Name)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&keep, "keep", false, "keep the local copy")
	f.BoolVar(&drop, "drop", false, "delete the local copy")

	return cmd
}

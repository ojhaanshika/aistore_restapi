// Package cli provides easy-to-use commands to manage, monitor, and utilize AIS clusters.
/*
 * Copyright (c) 2018-2025, NVIDIA CORPORATION. All rights reserved.
 */
package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/NVIDIA/aistore/api"
	"github.com/NVIDIA/aistore/api/apc"
	"github.com/NVIDIA/aistore/cmn"
	"github.com/NVIDIA/aistore/cmn/cos"

	"github.com/urfave/cli"
)

// Return `bckFrom` and `bckTo` - the [shift] and the [shift+1] arguments, respectively
// Optionally, return `objFrom` and/or `objTo`
func parseFromToURIs(c *cli.Context, bckFromArg, bckToArg string, shift int, optSrcOname, optDstOname bool) (bckFrom, bckTo cmn.Bck, objFrom, objTo string,
	err error) {
	if c.NArg() == shift {
		err = missingArgumentsError(c, bckFromArg, bckToArg)
		return cmn.Bck{}, cmn.Bck{}, "", "", err
	}
	if c.NArg() == shift+1 {
		err = missingArgumentsError(c, bckToArg)
		return cmn.Bck{}, cmn.Bck{}, "", "", err
	}

	// src
	var uri string
	if optSrcOname {
		uri = c.Args().Get(shift)
		bckFrom, objFrom, err = parseBckObjURI(c, uri, true /*emptyObjnameOK*/)
	} else {
		uri = c.Args().Get(shift)
		bckFrom, err = parseBckURI(c, uri, true /*error only*/)
	}
	if err != nil {
		if errV := errBucketNameInvalid(c, uri, err); errV != nil {
			err = errV
		} else {
			err = incorrectUsageMsg(c, "invalid %s argument '%s' - %v", bckFromArg, c.Args().Get(shift), err)
		}
		return cmn.Bck{}, cmn.Bck{}, "", "", err
	}

	// dst
	uri = c.Args().Get(shift + 1)
	if optDstOname {
		bckTo, objTo, err = parseBckObjURI(c, uri, true /*emptyObjnameOK*/)
	} else {
		bckTo, err = parseBckURI(c, uri, true)
	}
	if err != nil {
		if errV := errBucketNameInvalid(c, uri, err); errV != nil {
			err = errV
		} else {
			err = incorrectUsageMsg(c, "invalid %s argument '%s' - %v", bckToArg, c.Args().Get(shift+1), err)
		}
		return cmn.Bck{}, cmn.Bck{}, "", "", err
	}

	return bckFrom, bckTo, objFrom, objTo, err
}

func parseBckURI(c *cli.Context, uri string, errorOnly bool) (cmn.Bck, error) {
	const validNames = ": ais://mmm, s3://nnn or aws://nnn, gs://ppp, gcp://ppp, oc://ppp or oci://ppp"
	if isWebURL(uri) {
		bck := parseURLtoBck(uri)
		return bck, nil
	}

	opts := cmn.ParseURIOpts{}
	if !providerRequired {
		opts.DefaultProvider = cfg.DefaultProvider
	}
	bck, objName, err := cmn.ParseBckObjectURI(uri, opts)
	switch {
	case err != nil:
		if errV := errBucketNameInvalid(c, uri, err); errV != nil {
			err = errV
		}
		return cmn.Bck{}, err
	case objName != "":
		if errorOnly {
			return cmn.Bck{}, fmt.Errorf("unexpected object name argument %q", objName)
		}
		return cmn.Bck{}, objectNameArgNotExpected(c, objName)
	case bck.Name == "":
		if errorOnly {
			return cmn.Bck{}, fmt.Errorf("missing bucket name: %q", uri)
		}
		return cmn.Bck{}, incorrectUsageMsg(c, "missing bucket name in %q", uri)
	default:
		if err = bck.Validate(); err != nil {
			if errorOnly {
				return cmn.Bck{}, err
			}
			msg := "E.g. " + bucketArgument + validNames
			return cmn.Bck{}, cannotExecuteError(c, err, msg)
		}
	}
	return bck, nil
}

// `ais ls` and friends: allow for `provider:` shortcut
func preparseBckObjURI(uri string) string {
	if uri == "" {
		return uri
	}
	p := strings.TrimSuffix(uri, ":")
	if _, err := cmn.NormalizeProvider(p); err == nil {
		return p + apc.BckProviderSeparator
	}
	return uri // unchanged
}

func parseBckObjAux(c *cli.Context, uri string) (bck cmn.Bck, objnameOrPrefix string, err error) {
	bck, objnameOrPrefix, err = parseBckObjURI(c, uri, true /*optional objName*/)
	if err != nil {
		return bck, "", err
	}
	if bck.IsHT() {
		return bck, "", errors.New("http bucket is not supported as destination")
	}
	return bck, strings.Trim(objnameOrPrefix, "/"), nil
}

func parseQueryBckURI(uri string) (cmn.QueryBcks, string, error) {
	uri = preparseBckObjURI(uri)
	if isWebURL(uri) {
		bck := parseURLtoBck(uri)
		return cmn.QueryBcks(bck), "", nil
	}
	bck, prefix, err := cmn.ParseBckObjectURI(uri, cmn.ParseURIOpts{IsQuery: true})
	if prefix != "" && bck.IsQuery() {
		return cmn.QueryBcks(bck), prefix,
			fmt.Errorf("bucket query (%q) with embedded prefix (%q) is not supported", bck.String(), prefix)
	}
	return cmn.QueryBcks(bck), prefix, err
}

func parseBckObjURI(c *cli.Context, uri string, emptyObjnameOK bool) (bck cmn.Bck, objName string, err error) {
	if isWebURL(uri) {
		var hbo *cmn.HTTPBckObj
		hbo, err = cmn.NewHTTPObjPath(uri)
		if err != nil {
			return bck, "", err
		}
		bck, objName = hbo.Bck, hbo.ObjName
	} else {
		var opts cmn.ParseURIOpts
		if !providerRequired {
			opts.DefaultProvider = cfg.DefaultProvider
		}
		bck, objName, err = cmn.ParseBckObjectURI(uri, opts)
		if err != nil {
			if errV := errBucketNameInvalid(c, uri, err); errV != nil {
				return bck, objName, errV
			}
			var msg string
			if emptyObjnameOK {
				msg = "Expecting " + optionalObjectsArgument + ", e.g.: ais://mmm, s3://nnn/obj2, gs://ppp/a/b/c, etc."
			} else {
				msg = "Expecting " + objectArgument + ", e.g.: ais://mmm/obj1, s3://nnn/obj2, gs://ppp/obj3, etc."
			}
			return cmn.Bck{}, "", cannotExecuteError(c, err, msg)
		}
	}

	if bck.Name == "" {
		err = incorrectUsageMsg(c, "%q: missing bucket name", uri)
	} else if err = bck.Validate(); err != nil {
		if errV := errBucketNameInvalid(c, uri, err); errV != nil {
			err = errV
		} else {
			err = cannotExecuteError(c, err, "")
		}
	} else if objName == "" && !emptyObjnameOK {
		err = incorrectUsageMsg(c, "%q: missing object name", uri)
	}
	if err != nil {
		return cmn.Bck{}, "", err
	}
	return bck, objName, nil
}

//
// - handle (obj names) list, template (range), embedded prefix, and single object name
// - possibly call list-objects (via `lsObjVsPref`) to disambiguate
//

type (
	// disambiguate objname vs prefix
	dop struct {
		isObj    bool
		isPref   bool
		notFound bool
	}
	// parsing result
	oltp struct {
		objName  string
		list     string
		tmpl     string
		notFound bool
	}
)

func lsObjVsPref(bck cmn.Bck, oname string) (dop dop, _ error) {
	msg := &apc.LsoMsg{Prefix: oname}

	// NOTE: never "cached" (apc.LsCached)
	msg.SetFlag(apc.LsNameOnly)
	msg.SetFlag(apc.LsNoRecursion)
	lst, err := api.ListObjectsPage(apiBP, bck, msg, api.ListArgs{Limit: 32})

	if err != nil {
		return dop, V(err)
	}
	if len(lst.Entries) == 0 {
		dop.isObj, dop.notFound = true, true
		return dop, nil
	}

	for _, en := range lst.Entries {
		if en.Name == oname {
			dop.isObj = true
			break
		}
	}
	dop.isPref = len(lst.Entries) > 1 || !dop.isObj
	return dop, nil
}

func dopOLTP(c *cli.Context, bck cmn.Bck, objNameOrTmpl string) (oltp oltp, err error) {
	var prefix string
	if flagIsSet(c, listFlag) {
		oltp.list = parseStrFlag(c, listFlag)
	}
	if flagIsSet(c, templateFlag) {
		oltp.tmpl = parseStrFlag(c, templateFlag)
	}

	// when template is a "pure" prefix (use '--prefix' to disambiguate vs. objName)
	if flagIsSet(c, verbObjPrefixFlag) {
		prefix = parseStrFlag(c, verbObjPrefixFlag)
		if oltp.tmpl != "" {
			err = incorrectUsageMsg(c, errFmtExclusive, qflprn(verbObjPrefixFlag), qflprn(templateFlag))
			return oltp, err
		}
		oltp.tmpl = prefix
	}

	if oltp.list != "" && oltp.tmpl != "" {
		err = incorrectUsageMsg(c, errFmtExclusive, qflprn(listFlag), qflprn(templateFlag))
		return oltp, err
	}
	if objNameOrTmpl == "" {
		return oltp, err
	}

	switch {
	case oltp.list != "" || oltp.tmpl != "":
		what := "object name or prefix"
		if isPattern(objNameOrTmpl) {
			what = "pattern or template"
		}
		err = fmt.Errorf("%s (%s) cannot be used together with flags %s and %s (tip: use one or the other)",
			what, objNameOrTmpl, qflprn(listFlag), qflprn(templateFlag))
		return oltp, err
	case isPattern(objNameOrTmpl):
		oltp.tmpl = objNameOrTmpl

	case flagIsSet(c, nonRecursFlag) && !cos.IsLastB(objNameOrTmpl, '/'):
		// ambiguity #1
		warn := fmt.Sprintf("ambiguity resolving %q as an object name or embedded prefix - use %s to disambiguate",
			objNameOrTmpl, qflprn(verbObjPrefixFlag))
		actionWarn(c, warn)
		briefPause(1)
		oltp.objName = objNameOrTmpl

	case len(objNameOrTmpl) > 255:
		// not running lsObjVsPref when "prefix-or-objname" is that long
		oltp.objName = objNameOrTmpl

	default:
		// [NOTE] calling api.ListObjectsPage to disambiguate embedded prefix vs object name
		dop, err := lsObjVsPref(bck, objNameOrTmpl)
		oltp.notFound = dop.notFound
		switch {
		case err != nil:
			return oltp, err

		// ambiguity #2
		case dop.isObj && dop.isPref:
			err := fmt.Errorf("part of the URI %q can be interpreted as an object name and/or mutli-object matching prefix\n"+
				"(Tip:  to disambiguate, use either %s or %s)", objNameOrTmpl, qflprn(nonRecursFlag), qflprn(verbObjPrefixFlag))
			return oltp, err

		case dop.isObj:
			oltp.objName = objNameOrTmpl
		case dop.isPref:
			// (operation on all 'prefix'-ed objects)
			oltp.tmpl, oltp.objName = objNameOrTmpl, ""
		}
	}

	return oltp, err
}

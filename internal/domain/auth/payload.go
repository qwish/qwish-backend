package auth

// userPayload is the one shape every auth response uses to describe a user.
//
// It exists because verify-otp and create-profile each built this map inline
// and each forgot a different institution field: verify-otp sent neither,
// create-profile sent the nested object without the id. The NumPie client
// reads both — institution_id gates the "My Campus" toggle on Assessments,
// institution carries the name on Profile — so a learner who had joined an
// institution appeared unjoined on the next sign-in.
//
// instName is looked up by the caller (it needs the database); an empty name
// with a set InstitutionID still yields an institution object, because the id
// is what the client filters on and a missing name is cosmetic.
func userPayload(u *UserProfile, instName string) map[string]interface{} {
	p := map[string]interface{}{
		"id":           u.ID,
		"full_name":    u.FullName,
		"display_name": u.DisplayName,
		"email":        u.Email,
		"role":         u.Role,
		"status":       u.Status,
	}
	if u.InstitutionID != nil && *u.InstitutionID != "" {
		p["institution_id"] = *u.InstitutionID
		p["institution"] = map[string]string{"id": *u.InstitutionID, "name": instName}
	}
	return p
}

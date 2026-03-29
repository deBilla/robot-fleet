package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListSkills returns all skills, optionally filtered by type.
func (s *PostgresStore) ListSkills(ctx context.Context, skillType string) ([]*SkillRecord, error) {
	query := `SELECT id, name, description, skill_type, required_joints, version, created_at FROM skills_catalog`
	args := []any{}
	if skillType != "" {
		query += ` WHERE skill_type = $1`
		args = append(args, skillType)
	}
	query += ` ORDER BY name`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var skills []*SkillRecord
	for rows.Next() {
		sk := &SkillRecord{}
		var joints []byte
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Description, &sk.SkillType, &joints, &sk.Version, &sk.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		_ = json.Unmarshal(joints, &sk.RequiredJoints) // best-effort: joints already fetched from DB
		skills = append(skills, sk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skill rows iteration: %w", err)
	}
	return skills, nil
}

// GetSkill retrieves a skill by ID.
func (s *PostgresStore) GetSkill(ctx context.Context, id string) (*SkillRecord, error) {
	sk := &SkillRecord{}
	var joints []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, skill_type, required_joints, version, created_at
		FROM skills_catalog WHERE id = $1
	`, id).Scan(&sk.ID, &sk.Name, &sk.Description, &sk.SkillType, &joints, &sk.Version, &sk.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get skill %s: %w", id, err)
	}
	_ = json.Unmarshal(joints, &sk.RequiredJoints) // best-effort: joints already fetched from DB
	return sk, nil
}

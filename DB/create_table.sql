-- MySQL Workbench Forward Engineering

SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0;
SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0;
SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION';

-- -----------------------------------------------------
-- Schema mydb
-- -----------------------------------------------------
-- -----------------------------------------------------
-- Schema new_db_collection
-- -----------------------------------------------------

-- -----------------------------------------------------
-- Schema new_db_collection
-- -----------------------------------------------------
CREATE SCHEMA IF NOT EXISTS `new_db_collection` ;
USE `new_db_collection` ;

-- -----------------------------------------------------
-- Table `new_db_collection`.`organization`
-- -----------------------------------------------------
CREATE TABLE IF NOT EXISTS `new_db_collection`.`organization` (
  `org_id` BIGINT NOT NULL AUTO_INCREMENT,
  `org_name` VARCHAR(45) NULL,
  PRIMARY KEY (`org_id`))
ENGINE = InnoDB;


-- -----------------------------------------------------
-- Table `new_db_collection`.`system_users`
-- -----------------------------------------------------
CREATE TABLE IF NOT EXISTS `new_db_collection`.`system_users` (
  `user_id` BIGINT NOT NULL AUTO_INCREMENT,
  `email` VARCHAR(50) NULL,
  `password` VARCHAR(255) NULL,
  `is_staff` VARCHAR(45) NULL,
  `name` VARCHAR(45) NULL,
  `avatar_url` VARCHAR(255) NULL,
  `quota_total` INT NULL,
  `space_usage` INT NULL,
  `organization_org_id` BIGINT NOT NULL,
  PRIMARY KEY (`user_id`),
  INDEX `fk_system_users_organization1_idx` (`organization_org_id` ASC) VISIBLE,
  CONSTRAINT `fk_system_users_organization1`
    FOREIGN KEY (`organization_org_id`)
    REFERENCES `new_db_collection`.`organization` (`org_id`)
    ON DELETE NO ACTION
    ON UPDATE NO ACTION)
ENGINE = InnoDB;


-- -----------------------------------------------------
-- Table `new_db_collection`.`authentication_tokens`
-- -----------------------------------------------------
CREATE TABLE IF NOT EXISTS `new_db_collection`.`authentication_tokens` (
  `token_id` BIGINT NOT NULL AUTO_INCREMENT,
  `user_id` BIGINT NULL,
  `auth_token` VARCHAR(255) NULL,
  `generated_at` DATETIME NULL,
  `expires_at` DATETIME NULL,
  PRIMARY KEY (`token_id`),
  INDEX `fk_authentication_token_system_users_idx` (`user_id` ASC) VISIBLE,
  CONSTRAINT `fk_authentication_token_system_users`
    FOREIGN KEY (`user_id`)
    REFERENCES `new_db_collection`.`system_users` (`user_id`)
    ON DELETE CASCADE
    ON UPDATE CASCADE)
ENGINE = InnoDB;


-- -----------------------------------------------------
-- Table `new_db_collection`.`library`
-- -----------------------------------------------------
CREATE TABLE IF NOT EXISTS `new_db_collection`.`library` (
  `library_id` BIGINT NOT NULL AUTO_INCREMENT,
  `user_id` BIGINT NULL,
  `library_name` VARCHAR(45) NULL,
  PRIMARY KEY (`library_id`),
  INDEX `fk_library_system_users1_idx` (`user_id` ASC) VISIBLE,
  CONSTRAINT `fk_library_system_users1`
    FOREIGN KEY (`user_id`)
    REFERENCES `new_db_collection`.`system_users` (`user_id`)
    ON DELETE NO ACTION
    ON UPDATE NO ACTION)
ENGINE = InnoDB;


-- -----------------------------------------------------
-- Table `new_db_collection`.`files`
-- -----------------------------------------------------
CREATE TABLE IF NOT EXISTS `new_db_collection`.`files` (
  `file_id` BIGINT NOT NULL AUTO_INCREMENT,
  `library_id` BIGINT NULL,
  `file_name` VARCHAR(45) NULL,
  PRIMARY KEY (`file_id`),
  INDEX `fk_files_library1_idx` (`library_id` ASC) VISIBLE,
  CONSTRAINT `fk_files_library1`
    FOREIGN KEY (`library_id`)
    REFERENCES `new_db_collection`.`library` (`library_id`)
    ON DELETE NO ACTION
    ON UPDATE NO ACTION)
ENGINE = InnoDB;


SET SQL_MODE=@OLD_SQL_MODE;
SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS;
SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS;

package example;

import org.mapstruct.Mapper;

@Mapper
public interface PersonMapper {
    PersonDto toDto(Person person);
}
